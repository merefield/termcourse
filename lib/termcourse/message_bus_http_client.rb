# frozen_string_literal: true

require "net/http"
require "message_bus/http_client"

module Termcourse
  class MessageBusHTTPClient < MessageBus::HTTPClient
    DEFAULT_OPEN_TIMEOUT = 10
    DEFAULT_READ_TIMEOUT = 120
    DEFAULT_WRITE_TIMEOUT = 10

    def initialize(base_url, open_timeout: DEFAULT_OPEN_TIMEOUT, read_timeout: DEFAULT_READ_TIMEOUT, write_timeout: DEFAULT_WRITE_TIMEOUT, **kwargs)
      @open_timeout = open_timeout
      @read_timeout = read_timeout
      @write_timeout = write_timeout
      super(base_url, **kwargs)
    end

    private

    def poll
      http = Net::HTTP.new(@uri.host, @uri.port)
      http.use_ssl = true if @uri.scheme == "https"
      http.open_timeout = @open_timeout if @open_timeout
      http.read_timeout = @read_timeout if @read_timeout
      http.write_timeout = @write_timeout if @write_timeout && http.respond_to?(:write_timeout=)

      request = Net::HTTP::Post.new(request_path, headers)
      request.body = poll_payload

      if @enable_long_polling
        buffer = +""

        http.request(request) do |response|
          response.read_body do |chunk|
            next if chunk.empty?

            buffer << chunk
            process_buffer(buffer)
          end
        end
      else
        response = http.request(request)
        notify_channels(JSON.parse(response.body))
      end
    end
  end
end
