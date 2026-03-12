# frozen_string_literal: true

require "set"
require "message_bus/http_client"

module Termcourse
  class LiveUpdates
    def initialize(base_url, headers:, current_user_id: nil, client: nil, debug: nil)
      @client = client || MessageBus::HTTPClient.new(base_url, headers: headers)
      @current_user_id = current_user_id
      @debug = debug
      @mutex = Mutex.new
      @filter = :latest
      @incoming_topic_ids = Set.new

      subscribe_channels
    end

    def start
      @client.start
    rescue StandardError => e
      debug_log("live_updates_start_error #{e.class}: #{e.message}")
      nil
    end

    def stop
      @client.stop
    rescue StandardError => e
      debug_log("live_updates_stop_error #{e.class}: #{e.message}")
      nil
    end

    def track!(filter)
      @mutex.synchronize do
        @filter = filter.to_sym
        @incoming_topic_ids.clear
      end
    end

    def incoming_count
      @mutex.synchronize { @incoming_topic_ids.length }
    end

    def has_incoming?
      incoming_count.positive?
    end

    private

    def subscribe_channels
      subscribe("/latest")
      return unless @current_user_id

      subscribe("/new")
      subscribe("/unread")
      subscribe("/unread/#{@current_user_id}")
    end

    def subscribe(channel)
      @client.subscribe(channel, last_message_id: -1) do |data, _message_id, _global_id|
        handle_message(channel, data)
      end
    rescue StandardError => e
      debug_log("live_updates_subscribe_error channel=#{channel} #{e.class}: #{e.message}")
    end

    def handle_message(channel, data)
      payload = data.is_a?(Hash) ? data : {}
      return unless count_message?(channel, payload)

      topic_id = payload["topic_id"].to_i
      return if topic_id <= 0

      @mutex.synchronize { @incoming_topic_ids << topic_id }
      debug_log("live_updates_incoming filter=#{current_filter} channel=#{channel} topic_id=#{topic_id}")
    end

    def count_message?(channel, data)
      case current_filter
      when :latest
        channel == "/latest" && data["message_type"] == "latest" ||
          channel == "/new" && data["message_type"] == "new_topic"
      when :new
        channel == "/new" && data["message_type"] == "new_topic"
      when :unread
        unread_channel?(channel) && data["message_type"] == "unread" && !private_message?(data)
      when :private
        unread_channel?(channel) && data["message_type"] == "unread" && private_message?(data)
      else
        false
      end
    end

    def current_filter
      @mutex.synchronize { @filter }
    end

    def unread_channel?(channel)
      channel == "/unread" || channel == "/unread/#{@current_user_id}"
    end

    def private_message?(data)
      data.dig("payload", "archetype").to_s == "private_message"
    end

    def debug_log(message)
      @debug&.call(message)
    end
  end
end
