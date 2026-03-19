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
      @topic_channel = nil
      @topic_created_post_ids = []
      @topic_created_post_id_set = Set.new
      @topic_changed_post_ids = []
      @topic_changed_post_id_set = Set.new
      @topic_refresh_requested = nil
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
      watchdog = @mutex.synchronize do
        @running = false
        thread = @watchdog_thread
        @watchdog_thread = nil
        thread
      end
      watchdog&.kill
      watchdog&.join(0.1)
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

    def watch_topic!(topic_id, last_message_id: nil)
      topic_key = topic_id.to_i
      return clear_topic! if topic_key <= 0

      channel = topic_channel_name(topic_key)
      client = nil
      previous_channel = nil
      last_message_id = normalize_last_message_id(last_message_id)

      @mutex.synchronize do
        if @topic_channel == channel
          @channel_positions[channel] = last_message_id unless last_message_id.nil?
          debug_log("live_updates_topic_watch_refresh topic_id=#{topic_key} last_message_id=#{last_message_id.inspect}")
          return
        end

        previous_channel = @topic_channel
        @topic_channel = channel
        clear_topic_state!
        @channel_positions[channel] = last_message_id unless last_message_id.nil?
        client = @client
      end

      client&.unsubscribe(previous_channel) if previous_channel
      debug_log("live_updates_topic_unsubscribe channel=#{previous_channel}") if previous_channel
      subscribe(channel, client: client, last_message_id: last_message_id)
      debug_log("live_updates_topic_watch topic_id=#{topic_key} last_message_id=#{last_message_id.inspect}")
    rescue StandardError => e
      debug_log("live_updates_topic_watch_error topic_id=#{topic_key} #{e.class}: #{e.message}")
      nil
    end

    def clear_topic!
      previous_channel = nil
      client = nil

      @mutex.synchronize do
        previous_channel = @topic_channel
        @topic_channel = nil
        clear_topic_state!
        @channel_positions.delete(previous_channel) if previous_channel
        client = @client
      end

      client&.unsubscribe(previous_channel) if previous_channel
      debug_log("live_updates_topic_clear channel=#{previous_channel}") if previous_channel
    rescue StandardError => e
      debug_log("live_updates_topic_clear_error #{e.class}: #{e.message}")
      nil
    end

    def consume_topic_post_ids(topic_id)
      topic_channel = topic_channel_name(topic_id)
      @mutex.synchronize do
        return [] unless @topic_channel == topic_channel

        ids = @topic_created_post_ids.dup
        @topic_created_post_ids.clear
        @topic_created_post_id_set.clear
        ids
      end
    end

    def consume_topic_changed_post_ids(topic_id)
      topic_channel = topic_channel_name(topic_id)
      @mutex.synchronize do
        return [] unless @topic_channel == topic_channel

        ids = @topic_changed_post_ids.dup
        @topic_changed_post_ids.clear
        @topic_changed_post_id_set.clear
        ids
      end
    end

    def consume_topic_refresh_request(topic_id)
      topic_key = topic_id.to_i
      @mutex.synchronize do
        requested = @topic_refresh_requested == topic_key
        @topic_refresh_requested = nil if requested
        requested
      end
    end

    def requeue_topic_refresh_request(topic_id)
      topic_key = topic_id.to_i
      return if topic_key <= 0

      @mutex.synchronize do
        return unless @topic_channel == topic_channel_name(topic_key)

        @topic_refresh_requested = topic_key
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

    def subscribe(channel, client: @client, last_message_id: nil)
      last_message_id = last_message_id_for(channel, explicit_last_message_id: last_message_id)
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
      if topic_channel?(channel)
        debug_log(
          "live_updates_topic_message channel=#{channel} message_id=#{message_id} " \
          "type=#{payload['type'].inspect} reload_topic=#{payload['reload_topic'].inspect} " \
          "refresh_stream=#{payload['refresh_stream'].inspect} id=#{payload['id'].inspect} " \
          "keys=#{payload.keys.sort.join(',')}"
        )
      end
      if notification_channel?(channel)
        update_unread_notification_count(payload)
        return
      end
      if topic_channel?(channel)
        handle_topic_message(channel, payload)
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

    def topic_channel?(channel)
      @mutex.synchronize { @topic_channel == channel }
    end

    def last_message_id_for(channel, explicit_last_message_id: nil)
      return explicit_last_message_id if explicit_last_message_id.is_a?(Integer)

      stored =
        @mutex.synchronize do
          @channel_positions[channel]
        end
      return stored unless stored.nil?

      return @notification_channel_position.to_i if notification_channel?(channel) && !@notification_channel_position.nil?

      -1
    end

    def normalize_last_message_id(value)
      return value if value.is_a?(Integer)

      Integer(value)
    rescue StandardError
      nil
    end

    def private_message?(data)
      data.dig("payload", "archetype").to_s == "private_message"
    end

    def handle_topic_message(channel, data)
      if data["reload_topic"] || data["refresh_stream"] || data["type"] == "destroyed"
        request_topic_refresh(channel)
        debug_log("live_updates_topic_refresh channel=#{channel} reason=#{data['type'] || 'reload_topic'}")
        return
      end

      if data["type"] == "stats" && data.key?("posts_count")
        queued_created_posts = @mutex.synchronize do
          @topic_channel == channel && @topic_created_post_ids.any?
        end
        if queued_created_posts
          debug_log("live_updates_topic_stats_ignored channel=#{channel} reason=created_post_already_queued")
          return
        end

        request_topic_refresh(channel)
        debug_log("live_updates_topic_refresh channel=#{channel} reason=stats posts_count=#{data['posts_count']}")
        return
      end

      post_id = data["id"].to_i
      topic_id = topic_id_from_channel(channel)
      return if topic_id <= 0

      case data["type"]
      when "created"
        return if post_id <= 0

        @mutex.synchronize do
          return unless @topic_channel == channel

          add_topic_created_post_id(post_id)
        end
        debug_log("live_updates_topic_created topic_id=#{topic_id} post_id=#{post_id}")
      when "acted", "liked", "unliked", "deleted", "recovered"
        return if post_id <= 0

        @mutex.synchronize do
          return unless @topic_channel == channel

          add_topic_changed_post_id(post_id)
        end
        debug_log("live_updates_topic_changed topic_id=#{topic_id} post_id=#{post_id} type=#{data['type']}")
      end
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
        @unread_notification_count = count.nil? ? nil : [count, 0].max
        @pm_unread_count = pm_count.nil? ? nil : [pm_count, 0].max
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
      channels = nil

      @mutex.synchronize do
        return unless @running
        return if @restarting

        @restarting = true
        old_client = @client
        channels = subscribed_channels(topic_channel: @topic_channel)
        list_refresh_needed = !resume_positions_trustworthy?(channels)
      end

      debug_log("live_updates_restart reason=#{reason} positions=#{channel_positions.inspect}")
      old_client&.stop

      new_client = @client_factory.call
      channels.each do |channel|
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
          @topic_refresh_requested = topic_id_from_channel(@topic_channel) if @topic_channel
        end
        @restarting = false
      end
    rescue StandardError => e
      @mutex.synchronize { @restarting = false }
      debug_log("live_updates_restart_error reason=#{reason} #{e.class}: #{e.message}")
      nil
    end

    def subscribed_channels(topic_channel: :__use_current__)
      channels = ["/latest"]
      if @current_user_id
        channels += ["/new", "/unread", "/unread/#{@current_user_id}", "/notification/#{@current_user_id}"]
      end

      topic_channel = @mutex.synchronize { @topic_channel } if topic_channel == :__use_current__
      channels << topic_channel if topic_channel
      channels
    end

    def resume_positions_trustworthy?(channels = subscribed_channels)
      channels.all? do |channel|
        position = @channel_positions[channel]
        position.is_a?(Integer) && position >= 0
      end
    end

    def clear_incoming!
      @incoming_topic_ids.clear
      @incoming_topic_order.clear
    end

    def clear_topic_state!
      @topic_created_post_ids.clear
      @topic_created_post_id_set.clear
      @topic_changed_post_ids.clear
      @topic_changed_post_id_set.clear
      @topic_refresh_requested = nil
    end

    def add_topic_created_post_id(post_id)
      return if @topic_created_post_id_set.include?(post_id)

      @topic_created_post_id_set << post_id
      @topic_created_post_ids << post_id
    end

    def add_topic_changed_post_id(post_id)
      return if @topic_changed_post_id_set.include?(post_id)

      @topic_changed_post_id_set << post_id
      @topic_changed_post_ids << post_id
    end

    def request_topic_refresh(channel)
      topic_id = topic_id_from_channel(channel)
      return if topic_id <= 0

      @mutex.synchronize do
        return unless @topic_channel == channel

        clear_topic_state!
        @topic_refresh_requested = topic_id
      end
    end

    def topic_channel_name(topic_id)
      "/topic/#{topic_id.to_i}"
    end

    def topic_id_from_channel(channel)
      channel.to_s[%r{\A/topic/(\d+)\z}, 1].to_i
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
