# frozen_string_literal: true

require_relative "test_helper"
require "termcourse/message_bus_http_client"

module Termcourse
  class MessageBusHTTPClientTest < Minitest::Test
    class FakeHTTP
      attr_accessor :use_ssl, :open_timeout, :read_timeout, :write_timeout
      attr_reader :request_object

      def initialize(response: nil, &block)
        @response = response
        @request_block = block
      end

      def request(request)
        @request_object = request

        if block_given?
          yield @response
        else
          @response
        end
      end
    end

    class FakeResponse
      attr_reader :body

      def initialize(body: nil, chunks: nil)
        @body = body
        @chunks = chunks
      end

      def read_body
        Array(@chunks).each { |chunk| yield chunk }
      end
    end

    def with_stubbed_net_http_new(fake_http)
      singleton = Net::HTTP.singleton_class
      original = Net::HTTP.method(:new)
      singleton.send(:define_method, :new) { |_host, _port| fake_http }
      yield
    ensure
      singleton.send(:define_method, :new) { |*args, **kwargs, &block| original.call(*args, **kwargs, &block) }
    end

    def test_poll_applies_timeouts_and_notifies_channels_for_non_long_polling
      client = MessageBusHTTPClient.new(
        "https://meta.discourse.org",
        enable_long_polling: false,
        headers: { "Cookie" => "_forum_session=abc" },
        open_timeout: 11,
        read_timeout: 22,
        write_timeout: 33
      )
      client.status = MessageBus::HTTPClient::STARTED
      client.subscribe("/latest", last_message_id: 5) { |_payload, _message_id, _global_id| nil }

      payload = [{ "channel" => "/latest", "message_id" => 6, "data" => { "topic_id" => 1 } }]
      response = FakeResponse.new(body: JSON.generate(payload))
      http = FakeHTTP.new(response: response)
      notified = nil
      client.define_singleton_method(:notify_channels) { |messages| notified = messages }

      with_stubbed_net_http_new(http) do
        client.send(:poll)
      end

      assert_equal true, http.use_ssl
      assert_equal 11, http.open_timeout
      assert_equal 22, http.read_timeout
      assert_equal 33, http.write_timeout
      assert_equal client.send(:poll_payload), http.request_object.body
      assert_equal payload, notified
    end

    def test_poll_streams_long_poll_chunks_through_process_buffer
      client = MessageBusHTTPClient.new(
        "https://meta.discourse.org",
        enable_long_polling: true,
        headers: { "Cookie" => "_forum_session=abc" }
      )
      client.status = MessageBus::HTTPClient::STARTED
      client.subscribe("/latest", last_message_id: 5) { |_payload, _message_id, _global_id| nil }

      response = FakeResponse.new(chunks: ["", "alpha", "beta", ""])
      http = FakeHTTP.new(response: response)
      buffers = []
      client.define_singleton_method(:process_buffer) { |buffer| buffers << buffer.dup }

      with_stubbed_net_http_new(http) do
        client.send(:poll)
      end

      assert_equal ["alpha", "alphabeta"], buffers
    end
  end
end
