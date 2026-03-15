# frozen_string_literal: true

require "set"
require "message_bus/http_client"

module Termcourse
  class LiveUpdates
    MAX_INCOMING_TOPIC_IDS = 500

    def initialize(base_url, headers:, current_user_id: nil, client: nil, debug: nil, max_incoming_topic_ids: MAX_INCOMING_TOPIC_IDS)
      @client = client || MessageBus::HTTPClient.new(base_url, headers: headers)
      @current_user_id = current_user_id
      @debug = debug
      @max_incoming_topic_ids = [max_incoming_topic_ids.to_i, 1].max
      @mutex = Mutex.new
      @filter = :latest
      @incoming_topic_ids = Set.new
      @incoming_topic_order = []
      @unread_notification_count = nil

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
        clear_incoming!
      end
    end

    def incoming_count
      @mutex.synchronize { @incoming_topic_ids.length }
    end

    def incoming_topic_ids
      @mutex.synchronize { @incoming_topic_order.dup }
    end

    def has_incoming?
      incoming_count.positive?
    end

    def clear_incoming(topic_ids = nil)
      @mutex.synchronize do
        if topic_ids.nil?
          clear_incoming!
        else
          ids = Array(topic_ids).map(&:to_i)
          @incoming_topic_order.reject! { |topic_id| ids.include?(topic_id) }
          @incoming_topic_ids.subtract(ids)
        end
      end
    end

    def unread_notification_count
      @mutex.synchronize { @unread_notification_count }
    end

    def set_unread_notification_count(count)
      @mutex.synchronize do
        @unread_notification_count = [count.to_i, 0].max
      end
    end

    private

    def subscribe_channels
      subscribe("/latest")
      return unless @current_user_id

      subscribe("/new")
      subscribe("/unread")
      subscribe("/unread/#{@current_user_id}")
      subscribe("/notification/#{@current_user_id}")
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
      if notification_channel?(channel)
        update_unread_notification_count(payload)
        return
      end
      return unless count_message?(channel, payload)

      topic_id = payload["topic_id"].to_i
      return if topic_id <= 0

      @mutex.synchronize { add_incoming_topic_id(topic_id) }
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

    def notification_channel?(channel)
      channel == "/notification/#{@current_user_id}"
    end

    def private_message?(data)
      data.dig("payload", "archetype").to_s == "private_message"
    end

    def update_unread_notification_count(data)
      previous_unread = @mutex.synchronize { @unread_notification_count }
      count =
        if data.key?("all_unread_notifications_count") && data.key?("new_personal_messages_notifications_count")
          data["all_unread_notifications_count"].to_i - data["new_personal_messages_notifications_count"].to_i
        elsif data.key?("unread_notifications")
          data["unread_notifications"].to_i
        else
          previous_unread
        end
      @mutex.synchronize { @unread_notification_count = [count, 0].max }
      debug_log("live_updates_notifications unread=#{@unread_notification_count}")
    end

    def clear_incoming!
      @incoming_topic_ids.clear
      @incoming_topic_order.clear
    end

    def add_incoming_topic_id(topic_id)
      return if @incoming_topic_ids.include?(topic_id)

      @incoming_topic_ids << topic_id
      @incoming_topic_order << topic_id

      while @incoming_topic_order.length > @max_incoming_topic_ids
        expired_topic_id = @incoming_topic_order.shift
        @incoming_topic_ids.delete(expired_topic_id)
      end
    end

    def debug_log(message)
      @debug&.call(message)
    end
  end
end
