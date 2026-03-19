# frozen_string_literal: true

require_relative "test_helper"

module Termcourse
  class LiveUpdatesTest < Minitest::Test
    FakeStats = Struct.new(:failed, :success)

    class FakeClient
      attr_reader :subscriptions, :stats
      attr_accessor :started, :stopped, :status

      def initialize
        @subscriptions = {}
        @stats = FakeStats.new(0, 0)
        @status = MessageBus::HTTPClient::STOPPED
        @started = false
        @stopped = false
      end

      def subscribe(channel, last_message_id:, &callback)
        subscriptions[channel] = { last_message_id: last_message_id, callback: callback }
      end

      def start
        self.started = true
        self.status = MessageBus::HTTPClient::STARTED
      end

      def stop
        self.stopped = true
        self.status = MessageBus::HTTPClient::STOPPED
      end
    end

    def test_subscribes_to_expected_channels_for_logged_in_user
      client = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: client,
        watchdog_interval: 999
      )

      assert_equal ["/latest", "/new", "/notification/42", "/unread", "/unread/42"], client.subscriptions.keys.sort

      updates.start
      updates.stop
      assert_equal true, client.started
      assert_equal true, client.stopped
    end

    def test_notification_channel_uses_server_provided_channel_position
      client = FakeClient.new
      LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        notification_channel_position: 123,
        client: client,
        watchdog_interval: 999
      )

      assert_equal 123, client.subscriptions.fetch("/notification/42").fetch(:last_message_id)
    end

    def test_latest_filter_counts_latest_and_new_topic_messages
      updates, client = build_updates(filter: :latest)

      emit(client, "/latest", "topic_id" => 10, "message_type" => "latest", "payload" => {})
      emit(client, "/new", "topic_id" => 11, "message_type" => "new_topic", "payload" => {})
      emit(client, "/unread", "topic_id" => 12, "message_type" => "unread", "payload" => {})

      assert_equal 2, updates.incoming_count
    end

    def test_unread_filter_counts_only_non_private_unread_messages
      updates, client = build_updates(filter: :unread)

      emit(client, "/unread", "topic_id" => 21, "message_type" => "unread", "payload" => { "archetype" => "regular" })
      emit(client, "/unread/42", "topic_id" => 22, "message_type" => "unread", "payload" => { "archetype" => "private_message" })
      emit(client, "/latest", "topic_id" => 23, "message_type" => "latest", "payload" => {})

      assert_equal 1, updates.incoming_count
    end

    def test_private_filter_counts_only_private_message_unread_messages
      updates, client = build_updates(filter: :private)

      emit(client, "/unread/42", "topic_id" => 31, "message_type" => "unread", "payload" => { "archetype" => "private_message" })
      emit(client, "/unread", "topic_id" => 32, "message_type" => "unread", "payload" => { "archetype" => "regular" })

      assert_equal 1, updates.incoming_count
    end

    def test_track_resets_current_count_and_deduplicates_topics
      updates, client = build_updates(filter: :latest)

      emit(client, "/latest", "topic_id" => 41, "message_type" => "latest", "payload" => {})
      emit(client, "/latest", "topic_id" => 41, "message_type" => "latest", "payload" => {})
      assert_equal 1, updates.incoming_count

      updates.track!(:new)
      assert_equal 0, updates.incoming_count

      emit(client, "/new", "topic_id" => 42, "message_type" => "new_topic", "payload" => {})
      assert_equal 1, updates.incoming_count
    end

    def test_incoming_topic_window_is_bounded
      client = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: client,
        max_incoming_topic_ids: 3,
        watchdog_interval: 999
      )
      updates.track!(:latest)

      emit(client, "/latest", "topic_id" => 51, "message_type" => "latest", "payload" => {})
      emit(client, "/latest", "topic_id" => 52, "message_type" => "latest", "payload" => {})
      emit(client, "/latest", "topic_id" => 53, "message_type" => "latest", "payload" => {})
      emit(client, "/latest", "topic_id" => 54, "message_type" => "latest", "payload" => {})

      assert_equal 3, updates.incoming_count

      emit(client, "/latest", "topic_id" => 51, "message_type" => "latest", "payload" => {})
      assert_equal 3, updates.incoming_count
    end

    def test_notification_channel_updates_unread_notification_count
      updates, client = build_updates(filter: :latest)

      emit(
        client,
        "/notification/42",
        "all_unread_notifications_count" => 7,
        "new_personal_messages_notifications_count" => 2
      )

      assert_equal 5, updates.unread_notification_count
      assert_equal 2, updates.pm_unread_count
    end

    def test_partial_notification_payload_preserves_previous_unread_count
      updates, client = build_updates(filter: :latest)
      updates.set_unread_notification_count(4)
      updates.set_pm_unread_count(1)

      emit(client, "/notification/42", "all_unread_notifications_count" => 7)

      assert_equal 4, updates.unread_notification_count
      assert_equal 1, updates.pm_unread_count
    end

    def test_unread_notification_count_is_nil_until_seeded
      updates, = build_updates(filter: :latest)

      assert_nil updates.unread_notification_count

      updates.set_unread_notification_count(0)

      assert_equal 0, updates.unread_notification_count
    end

    def test_first_partial_notification_payload_preserves_nil_state
      updates, client = build_updates(filter: :latest)

      emit(client, "/notification/42", "all_unread_notifications_count" => 7)

      assert_nil updates.unread_notification_count
      assert_nil updates.pm_unread_count
    end

    def test_can_seed_unread_notification_count
      updates, = build_updates(filter: :latest)

      updates.set_unread_notification_count(3)

      assert_equal 3, updates.unread_notification_count
    end

    def test_updates_channel_positions_from_message_ids
      updates, client = build_updates(filter: :latest)

      emit(client, "/latest", { "topic_id" => 99, "message_type" => "latest", "payload" => {} }, message_id: 321)

      assert_equal 321, updates.channel_positions.fetch("/latest")
    end

    def test_monitor_restarts_stale_client_from_saved_channel_positions
      clock = Time.utc(2026, 3, 19, 12, 0, 0)
      first = FakeClient.new
      second = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: first,
        client_factory: -> { second },
        watchdog_interval: 999,
        watchdog_stale_threshold: 240,
        now_proc: -> { clock }
      )
      updates.track!(:latest)
      updates.start

      emit(first, "/latest", { "topic_id" => 61, "message_type" => "latest", "payload" => {} }, message_id: 777)
      clock += 241
      updates.monitor(now: clock)

      assert_equal true, first.stopped
      assert_equal true, second.started
      assert_equal 777, second.subscriptions.fetch("/latest").fetch(:last_message_id)
      assert_equal true, updates.consume_resync_request

      updates.stop
    end

    def test_monitor_requests_topic_list_refresh_when_resume_positions_are_missing
      clock = Time.utc(2026, 3, 19, 12, 0, 0)
      first = FakeClient.new
      second = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: first,
        client_factory: -> { second },
        watchdog_interval: 999,
        watchdog_stale_threshold: 240,
        now_proc: -> { clock }
      )
      updates.track!(:latest)
      updates.start

      updates.instance_variable_get(:@channel_positions).delete("/latest")
      emit(first, "/latest", { "topic_id" => 61, "message_type" => "latest", "payload" => {} }, message_id: 777)
      updates.instance_variable_get(:@channel_positions).delete("/latest")

      clock += 241
      updates.monitor(now: clock)

      assert_equal true, updates.consume_topic_list_refresh_request
      assert_equal 0, updates.incoming_count

      updates.stop
    end

    def test_monitor_requests_topic_list_refresh_when_positions_are_only_default_lookahead
      clock = Time.utc(2026, 3, 19, 12, 0, 0)
      first = FakeClient.new
      second = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: first,
        client_factory: -> { second },
        watchdog_interval: 999,
        watchdog_stale_threshold: 240,
        now_proc: -> { clock }
      )
      updates.track!(:latest)
      updates.start

      clock += 241
      updates.monitor(now: clock)

      assert_equal true, updates.consume_topic_list_refresh_request
      assert_equal(-1, second.subscriptions.fetch("/latest").fetch(:last_message_id))

      updates.stop
    end

    private

    def build_updates(filter:)
      client = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: client,
        watchdog_interval: 999
      )
      updates.track!(filter)
      [updates, client]
    end

    def emit(client, channel, payload = nil, message_id: 100, **payload_kwargs)
      payload = payload_kwargs unless payload_kwargs.empty?
      payload ||= {}
      client.subscriptions.fetch(channel).fetch(:callback).call(payload, message_id, 200)
    end
  end
end
