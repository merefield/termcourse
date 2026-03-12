# frozen_string_literal: true

require_relative "test_helper"

module Termcourse
  class LiveUpdatesTest < Minitest::Test
    FakeClient = Struct.new(:subscriptions, :started, :stopped) do
      def subscribe(channel, last_message_id:, &callback)
        self.subscriptions ||= {}
        subscriptions[channel] = { last_message_id: last_message_id, callback: callback }
      end

      def start
        self.started = true
      end

      def stop
        self.stopped = true
      end
    end

    def test_subscribes_to_expected_channels_for_logged_in_user
      client = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: client
      )

      assert_equal ["/latest", "/new", "/unread", "/unread/42"], client.subscriptions.keys.sort

      updates.start
      updates.stop
      assert_equal true, client.started
      assert_equal true, client.stopped
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
        max_incoming_topic_ids: 3
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

    private

    def build_updates(filter:)
      client = FakeClient.new
      updates = LiveUpdates.new(
        "https://meta.discourse.org",
        headers: { "Cookie" => "_forum_session=abc" },
        current_user_id: 42,
        client: client
      )
      updates.track!(filter)
      [updates, client]
    end

    def emit(client, channel, payload)
      client.subscriptions.fetch(channel).fetch(:callback).call(payload, 100, 200)
    end
  end
end
