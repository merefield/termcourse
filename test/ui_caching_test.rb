# frozen_string_literal: true

require_relative "test_helper"

module Termcourse
  class UICachingTest < Minitest::Test
    FakeLiveUpdates = Struct.new(:unread_notification_count, :incoming_ids) do
      def incoming_count
        Array(incoming_ids).length
      end

      def set_unread_notification_count(count)
        self.unread_notification_count = count
      end

      def incoming_topic_ids
        Array(incoming_ids).dup
      end

      def clear_incoming(topic_ids = nil)
        ids = Array(topic_ids)
        self.incoming_ids = Array(incoming_ids) - ids
      end
    end

    class FakeClient
      attr_reader :list_calls, :topic_calls, :topic_posts_calls, :notification_totals_calls, :pm_calls, :post_calls, :mark_notification_read_calls

      def initialize
        @list_calls = []
        @topic_calls = []
        @topic_posts_calls = []
        @notification_totals_calls = 0
        @pm_calls = 0
        @post_calls = []
        @mark_notification_read_calls = []
      end

      def list_topics(filter, period:, username:, params: nil)
        @list_calls << { filter: filter, period: period, username: username, params: params }
        topic_ids = Array(params && params[:topic_ids]).map(&:to_i)
        topics =
          if topic_ids.empty?
            [{ "id" => 1, "title" => "One", "reply_count" => 0 }]
          else
            topic_ids.map { |id| { "id" => id, "title" => "Topic #{id}", "reply_count" => 0 } }
          end

        { "topic_list" => { "topics" => topics, "more_topics_url" => nil } }
      end

      def topic(topic_id, near_post: nil)
        @topic_calls << { topic_id: topic_id, near_post: near_post }
        {
          "id" => topic_id,
          "title" => "Topic #{topic_id}",
          "chunk_size" => 2,
          "highest_post_number" => 5,
          "post_stream" => {
            "stream" => [11, 12, 13, 14, 15],
            "posts" => [
              { "id" => 11, "post_number" => 1, "raw" => "a", "username" => "one" },
              { "id" => 12, "post_number" => 2, "raw" => "b", "username" => "one" }
            ]
          }
        }
      end

      def topic_posts(topic_id, post_ids:, include_raw: true)
        @topic_posts_calls << { topic_id: topic_id, post_ids: post_ids, include_raw: include_raw }
        {
          "post_stream" => {
            "posts" => Array(post_ids).map.with_index do |id, idx|
              { "id" => id, "post_number" => 3 + idx, "raw" => "post #{id}", "username" => "two" }
            end
          }
        }
      end

      def notification_totals
        @notification_totals_calls += 1
        { "unread_notifications" => 4 }
      end

      def get_url(path)
        @pm_calls += 1
        return { "topic_list" => { "topics" => [] } } if path == "/topics/private-messages-unread.json"

        {}
      end

      def post(post_id)
        @post_calls << post_id
        { "id" => post_id, "post_number" => 3, "raw" => "new post", "username" => "robert" }
      end

      def mark_notification_read(notification_id)
        @mark_notification_read_calls << notification_id
        { "success" => "OK" }
      end
    end

    def setup
      @client = FakeClient.new
      @ui = UI.allocate
      @ui.instance_variable_set(:@client, @client)
      @ui.instance_variable_set(:@api_username, "robert")
      @ui.instance_variable_set(:@topic_list_cache, {})
      @ui.instance_variable_set(:@topic_cache, {})
      @ui.instance_variable_set(:@pm_unread_refresh_interval, 30)
      @ui.instance_variable_set(:@notification_unread_refresh_interval, 30)
      @ui.instance_variable_set(:@pm_unread_count, 0)
      @ui.instance_variable_set(:@notification_unread_count, 0)
      @ui.instance_variable_set(:@locale, "en")
      @ui.instance_variable_set(:@theme, UI::BUILTIN_THEMES["default"])
      @ui.instance_variable_set(:@color_mode, "truecolor")
      @ui.instance_variable_set(:@live_updates, nil)
      @ui.define_singleton_method(:with_errors) { |&block| block.call }
    end

    def test_load_list_data_reuses_cached_list_until_invalidated
      @ui.send(:load_list_data, :latest, :monthly)
      @ui.send(:load_list_data, :latest, :monthly)

      assert_equal 1, @client.list_calls.length
    end

    def test_load_list_data_uses_incoming_topic_ids_for_incremental_refresh
      @ui.send(:load_list_data, :latest, :monthly)
      @ui.instance_variable_set(:@live_updates, FakeLiveUpdates.new(nil, [7]))

      data = @ui.send(:load_list_data, :latest, :monthly)

      assert_equal 2, @client.list_calls.length
      assert_equal [7], @client.list_calls.last[:params][:topic_ids]
      assert_equal [7, 1], data.dig("topic_list", "topics").map { |topic| topic["id"] }
    end

    def test_load_topic_data_reuses_cached_topic
      @ui.send(:load_topic_data, 42)
      @ui.send(:load_topic_data, 42)

      assert_equal 1, @client.topic_calls.length
    end

    def test_topic_loop_forces_refresh_when_opened_from_list
      calls = []
      @ui.define_singleton_method(:load_topic_data) do |topic_id, near_post: nil, force: false|
        calls << { topic_id: topic_id, near_post: near_post, force: force }
        nil
      end

      @ui.send(:topic_loop, 42)

      assert_equal [{ topic_id: 42, near_post: nil, force: true }], calls
    end

    def test_ensure_topic_chunk_loaded_fetches_only_missing_posts
      topic_data = @ui.send(:load_topic_data, 42)

      @ui.send(:ensure_topic_chunk_loaded, 42, topic_data, 1)

      assert_equal 1, @client.topic_posts_calls.length
      assert_equal [13, 14], @client.topic_posts_calls.first[:post_ids]
      assert_equal [11, 12, 13, 14], topic_data.dig("post_stream", "posts").map { |post| post["id"] }
    end

    def test_load_next_topic_chunk_ignores_failed_fetch
      topic_data = @ui.send(:load_topic_data, 42)
      @ui.define_singleton_method(:fetch_topic_posts) { |_topic_id, _post_ids| nil }

      added = @ui.send(:load_next_topic_chunk, 42, topic_data, 1)

      assert_equal 0, added
      assert_equal [11, 12], topic_data.dig("post_stream", "posts").map { |post| post["id"] }
    end

    def test_load_previous_topic_chunk_ignores_failed_fetch
      topic_data = @ui.send(:load_topic_data, 42)
      topic_data["post_stream"]["posts"] = [
        { "id" => 13, "post_number" => 3, "raw" => "c", "username" => "two" },
        { "id" => 14, "post_number" => 4, "raw" => "d", "username" => "two" }
      ]
      @ui.define_singleton_method(:fetch_topic_posts) { |_topic_id, _post_ids| nil }

      added = @ui.send(:load_previous_topic_chunk, 42, topic_data, 1)

      assert_equal 0, added
      assert_equal [13, 14], topic_data.dig("post_stream", "posts").map { |post| post["id"] }
    end

    def test_merge_topic_list_data_appends_paginated_results_in_order
      existing = {
        "topic_list" => {
          "topics" => [
            { "id" => 1, "title" => "One" },
            { "id" => 2, "title" => "Two" }
          ],
          "more_topics_url" => "/latest?page=1"
        }
      }
      incoming = {
        "topic_list" => {
          "topics" => [
            { "id" => 3, "title" => "Three" },
            { "id" => 4, "title" => "Four" }
          ],
          "more_topics_url" => "/latest?page=2"
        }
      }

      @ui.send(:merge_topic_list_data!, existing, incoming)

      assert_equal [1, 2, 3, 4], existing.dig("topic_list", "topics").map { |topic| topic["id"] }
      assert_equal "/latest?page=2", existing.dig("topic_list", "more_topics_url")
    end

    def test_maybe_refresh_notification_unread_count_skips_poll_when_live_updates_seeded
      @ui.instance_variable_set(:@notification_unread_count, 4)
      @ui.instance_variable_set(:@live_updates, FakeLiveUpdates.new(4, []))

      now = Time.utc(2026, 3, 15, 12, 0, 0)
      @ui.send(:maybe_refresh_notification_unread_count, now: now)
      @ui.send(:maybe_refresh_notification_unread_count, now: now + 10)

      assert_equal 0, @client.notification_totals_calls
    end

    def test_maybe_refresh_pm_unread_count_uses_ttl
      now = Time.utc(2026, 3, 15, 12, 0, 0)

      @ui.send(:maybe_refresh_pm_unread_count, now: now)
      @ui.send(:maybe_refresh_pm_unread_count, now: now + 10)
      @ui.send(:maybe_refresh_pm_unread_count, now: now + 31)

      assert_equal 2, @client.pm_calls
    end

    def test_mark_notification_read_decrements_local_and_live_unread_counts
      live_updates = FakeLiveUpdates.new(4, [])
      notification = { "id" => 99, "read" => false }
      @ui.instance_variable_set(:@notification_unread_count, 4)
      @ui.instance_variable_set(:@live_updates, live_updates)

      @ui.send(:mark_notification_read, notification)

      assert_equal true, notification["read"]
      assert_equal [99], @client.mark_notification_read_calls
      assert_equal 3, @ui.instance_variable_get(:@notification_unread_count)
      assert_equal 3, live_updates.unread_notification_count
    end

    def test_append_created_post_to_topic_uses_single_post_fetch
      topic_data = @ui.send(:load_topic_data, 42)

      @ui.send(:append_created_post_to_topic, 42, topic_data, { "id" => 16, "post_number" => 3 })

      assert_equal [16], @client.post_calls
      assert_equal [11, 12, 16], topic_data.dig("post_stream", "posts").map { |post| post["id"] }
    end
  end
end
