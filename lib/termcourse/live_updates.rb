# frozen_string_literal: true

require "set"
require "message_bus/http_client"
require "termcourse/message_bus_http_client"

module Termcourse
  class LiveUpdates
    MAX_INCOMING_TOPIC_IDS = 500
    WATCHDOG_INTERVAL = 30
    WATCHDOG_STALE_THRESHOLD = 240

    def initialize(
      base_url,
      headers:,
      current_user_id: nil,
      notification_channel_position: nil,
      client: nil,
      client_factory: nil,
      debug: nil,
      max_incoming_topic_ids: MAX_INCOMING_TOPIC_IDS,
      watchdog_interval: WATCHDOG_INTERVAL,
      watchdog_stale_threshold: WATCHDOG_STALE_THRESHOLD,
      now_proc: nil
    )
      @base_url = base_url
      @headers = headers
      @current_user_id = current_user_id
      @notification_channel_position = notification_channel_position
      @client = client || build_client_from(client_factory)
      @client_factory = client_factory || default_client_factory
      @debug = debug
      @max_incoming_topic_ids = [max_incoming_topic_ids.to_i, 1].max
      @watchdog_interval = [watchdog_interval.to_i, 1].max
      @watchdog_stale_threshold = [watchdog_stale_threshold.to_i, 1].max
      @now_proc = now_proc || -> { Time.now }
      @mutex = Mutex.new
      @filter = :latest
      @incoming_topic_ids = Set.new
      @incoming_topic_order = []
      @unread_notification_count = nil
      @pm_unread_count = nil
      @channel_positions = {}
      @last_success_at = nil
      @last_success_count = 0
      @last_message_at = nil
      @watchdog_thread = nil
      @running = false
      @resync_requested = false
      @topic_list_refresh_requested = false
      @restarting = false

      subscribe_channels
    end

    def start
      @mutex.synchronize do
        return if @running

        @running = true
        @last_success_at = current_time
        @last_success_count = client_success_count(@client)
      end

      @client.start
      start_watchdog
    rescue StandardError => e
      debug_log("live_updates_start_error #{e.class}: #{e.message}")
      nil
    end

    def stop
      watchdog = @mutex.synchronize do
        @running = false
        thread = @watchdog_thread
        @watchdog_thread = nil
        thread
      end

      @client.stop
      watchdog&.kill
      watchdog&.join(0.1)
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

    def pm_unread_count
      @mutex.synchronize { @pm_unread_count }
    end

    def set_pm_unread_count(count)
      @mutex.synchronize do
        @pm_unread_count = [count.to_i, 0].max
      end
    end

    def consume_resync_request
      @mutex.synchronize do
        requested = @resync_requested
        @resync_requested = false
        requested
      end
    end

    def consume_topic_list_refresh_request
      @mutex.synchronize do
        requested = @topic_list_refresh_requested
        @topic_list_refresh_requested = false
        requested
      end
    end

    def monitor(now: current_time)
      client = nil
      restart_reason = nil

      @mutex.synchronize do
        return unless @running

        client = @client
        success_count = client_success_count(client)
        if success_count > @last_success_count
          @last_success_count = success_count
          @last_success_at = now
        end

        status = client_status(client)
        if status != MessageBus::HTTPClient::STARTED
          restart_reason = :stopped
        elsif @last_success_at && (now - @last_success_at) > @watchdog_stale_threshold
          restart_reason = :stale
        end
      end

      restart_client(reason: restart_reason, now: now) if restart_reason
    rescue StandardError => e
      debug_log("live_updates_watchdog_error #{e.class}: #{e.message}")
      nil
    end

    def channel_positions
      @mutex.synchronize { @channel_positions.dup }
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

    def subscribe(channel, client: @client)
      last_message_id = last_message_id_for(channel)
      @mutex.synchronize { @channel_positions[channel] = last_message_id unless @channel_positions.key?(channel) }
      client.subscribe(channel, last_message_id: last_message_id) do |data, message_id, _global_id|
        handle_message(channel, data, message_id: message_id)
      end
    rescue StandardError => e
      debug_log("live_updates_subscribe_error channel=#{channel} #{e.class}: #{e.message}")
    end

    def handle_message(channel, data, message_id:)
      payload = data.is_a?(Hash) ? data : {}
      @mutex.synchronize do
        @channel_positions[channel] = message_id if message_id.is_a?(Integer)
        @last_message_at = current_time
      end
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

    def last_message_id_for(channel)
      stored =
        @mutex.synchronize do
          @channel_positions[channel]
        end
      return stored unless stored.nil?

      return @notification_channel_position.to_i if notification_channel?(channel) && !@notification_channel_position.nil?

      -1
    end

    def private_message?(data)
      data.dig("payload", "archetype").to_s == "private_message"
    end

    def update_unread_notification_count(data)
      previous_unread, previous_pm = @mutex.synchronize { [@unread_notification_count, @pm_unread_count] }
      pm_count =
        if data.key?("new_personal_messages_notifications_count")
          data["new_personal_messages_notifications_count"].to_i
        elsif data.key?("unread_personal_messages")
          data["unread_personal_messages"].to_i
        else
          previous_pm
        end
      count =
        if data.key?("all_unread_notifications_count") && data.key?("new_personal_messages_notifications_count")
          data["all_unread_notifications_count"].to_i - data["new_personal_messages_notifications_count"].to_i
        elsif data.key?("all_unread_notifications_count") && data.key?("unread_personal_messages")
          data["all_unread_notifications_count"].to_i - data["unread_personal_messages"].to_i
        elsif data.key?("unread_notifications")
          data["unread_notifications"].to_i
        else
          previous_unread
        end
      @mutex.synchronize do
        @unread_notification_count = [count, 0].max
        @pm_unread_count = [pm_count, 0].max
      end
      debug_log("live_updates_notifications unread=#{@unread_notification_count} pm_unread=#{@pm_unread_count}")
    end

    def build_client_from(factory)
      return factory.call if factory

      default_client_factory.call
    end

    def default_client_factory
      headers = @headers
      base_url = @base_url
      lambda do
        Termcourse::MessageBusHTTPClient.new(base_url, headers: headers)
      end
    end

    def current_time
      @now_proc.call
    end

    def client_success_count(client)
      client&.stats&.success.to_i
    rescue StandardError
      0
    end

    def client_status(client)
      client&.status
    rescue StandardError
      nil
    end

    def start_watchdog
      thread = Thread.new do
        while running?
          sleep @watchdog_interval
          monitor
        end
      rescue StandardError => e
        debug_log("live_updates_watchdog_thread_error #{e.class}: #{e.message}")
      end
      thread.abort_on_exception = true
      @mutex.synchronize { @watchdog_thread = thread }
    end

    def running?
      @mutex.synchronize { @running }
    end

    def restart_client(reason:, now:)
      old_client = nil
      new_client = nil
      list_refresh_needed = false

      @mutex.synchronize do
        return unless @running
        return if @restarting

        @restarting = true
        old_client = @client
        list_refresh_needed = !resume_positions_trustworthy?
      end

      debug_log("live_updates_restart reason=#{reason} positions=#{channel_positions.inspect}")
      old_client&.stop

      new_client = @client_factory.call
      subscribed_channels.each do |channel|
        subscribe(channel, client: new_client)
      end
      new_client.start

      @mutex.synchronize do
        @client = new_client
        @last_success_count = client_success_count(new_client)
        @last_success_at = now
        @resync_requested = true
        if list_refresh_needed
          clear_incoming!
          @topic_list_refresh_requested = true
        end
        @restarting = false
      end
    rescue StandardError => e
      @mutex.synchronize { @restarting = false }
      debug_log("live_updates_restart_error reason=#{reason} #{e.class}: #{e.message}")
      nil
    end

    def subscribed_channels
      channels = ["/latest"]
      return channels unless @current_user_id

      channels + ["/new", "/unread", "/unread/#{@current_user_id}", "/notification/#{@current_user_id}"]
    end

    def resume_positions_trustworthy?
      subscribed_channels.all? do |channel|
        position = @channel_positions[channel]
        position.is_a?(Integer) && position >= 0
      end
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
