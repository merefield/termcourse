# frozen_string_literal: true

require_relative "test_helper"

module Termcourse
  class LocalizationTest < Minitest::Test
    def test_resolve_locale_supports_language_prefix
      assert_equal "fr", Localization.resolve_locale("fr_FR.UTF-8")
      assert_equal "de", Localization.resolve_locale("de_DE.UTF-8")
      assert_equal "es", Localization.resolve_locale("es_ES.UTF-8")
      assert_equal "en", Localization.resolve_locale("it_IT.UTF-8")
    end

    def test_ui_uses_selected_locale_for_labels
      ui = UI.allocate
      ui.instance_variable_set(:@locale, "fr")
      ui.instance_variable_set(:@api_username, "robert")
      ui.instance_variable_set(:@notification_unread_count, 0)
      ui.instance_variable_set(:@live_updates, nil)
      ui.instance_variable_set(:@theme, UI::BUILTIN_THEMES["default"])
      ui.instance_variable_set(:@color_mode, "truecolor")

      assert_equal "Messages privés", ui.send(:topic_list_filter_label, :private)
      assert_includes ui.send(:strip_all_ansi, ui.send(:login_label)), "Connecté : robert"
    end
  end

  class UIScreenRendererTest < Minitest::Test
    def setup
      @renderer = UI::ScreenRenderer.new(pad_line: ->(line, width) { line.ljust(width) })
    end

    def test_first_render_repaints_full_screen
      output = capture_stdout do
        @renderer.render(
          ["one", "two"],
          width: 5,
          height: 2,
          view_key: :topic_list
        )
      end

      assert_includes output, TTY::Cursor.clear_screen
      assert_includes output, "one  "
      assert_includes output, "two  "
    end

    def test_second_render_only_repaints_changed_rows
      capture_stdout do
        @renderer.render(
          ["one", "two"],
          width: 5,
          height: 2,
          view_key: :topic_list
        )
      end

      output = capture_stdout do
        @renderer.render(
          ["one", "too"],
          width: 5,
          height: 2,
          view_key: :topic_list
        )
      end

      refute_includes output, TTY::Cursor.clear_screen
      refute_includes output, "one  "
      assert_includes output, "too  "
    end

    def test_view_key_change_forces_full_repaint
      capture_stdout do
        @renderer.render(
          ["one"],
          width: 5,
          height: 1,
          view_key: :topic_list
        )
      end

      output = capture_stdout do
        @renderer.render(
          ["one"],
          width: 5,
          height: 1,
          view_key: :search
        )
      end

      assert_includes output, TTY::Cursor.clear_screen
    end

    def test_render_shows_cursor_when_cursor_position_is_provided
      output = capture_stdout do
        @renderer.render(
          ["one"],
          width: 5,
          height: 1,
          view_key: :search_prompt,
          cursor: { x: 3, y: 0 }
        )
      end

      assert_includes output, TTY::Cursor.move_to(3, 0)
      assert_includes output, TTY::Cursor.show
    end

    private

    def capture_stdout
      stdout = $stdout
      fake = StringIO.new
      $stdout = fake
      yield
      fake.string
    ensure
      $stdout = stdout
    end
  end

  class UIEscapeSequenceTest < Minitest::Test
    FakeReader = Struct.new(:responses) do
      def read_keypress(**)
        responses.shift
      end
    end

    def test_read_escape_sequence_returns_plain_key_unchanged
      ui = build_ui_with_reader([])

      assert_equal "j", ui.send(:read_escape_sequence, "j")
    end

    def test_read_escape_sequence_returns_escape_when_sequence_is_incomplete
      ui = build_ui_with_reader([])

      assert_equal "\e", ui.send(:read_escape_sequence, "\e")
    end

    def test_read_escape_sequence_assembles_arrow_key_sequence
      ui = build_ui_with_reader(["[", "B", nil])

      assert_equal "\e[B", ui.send(:read_escape_sequence, "\e")
    end

    private

    def build_ui_with_reader(responses)
      ui = UI.allocate
      ui.instance_variable_set(:@reader, FakeReader.new(responses))
      ui
    end
  end

  class UIOutputHelpersTest < Minitest::Test
    FakeRenderer = Struct.new(:reset_called, :render_args) do
      def reset!
        self.reset_called = true
      end

      def render(*args, **kwargs)
        self.render_args = [args, kwargs]
      end
    end

    def test_clear_screen_resets_renderer_and_clears_terminal
      renderer = FakeRenderer.new(false, nil)
      ui = UI.allocate
      ui.instance_variable_set(:@renderer, renderer)

      output = capture_stdout { ui.send(:clear_screen) }

      assert renderer.reset_called
      assert_includes output, TTY::Cursor.show
      assert_includes output, TTY::Cursor.clear_screen
      assert_includes output, TTY::Cursor.move_to(0, 0)
    end

    def test_render_screen_delegates_to_renderer
      renderer = FakeRenderer.new(false, nil)
      ui = UI.allocate
      ui.instance_variable_set(:@renderer, renderer)

      ui.send(:render_screen, ["alpha"], width: 10, height: 3, view_key: :topic_list, cursor: { x: 2, y: 1 }, force: true)

      args, kwargs = renderer.render_args
      assert_equal [["alpha"]], args
      assert_equal 10, kwargs[:width]
      assert_equal 3, kwargs[:height]
      assert_equal :topic_list, kwargs[:view_key]
      assert_equal({ x: 2, y: 1 }, kwargs[:cursor])
      assert_equal true, kwargs[:force]
    end

    private

    def capture_stdout
      stdout = $stdout
      fake = StringIO.new
      $stdout = fake
      yield
      fake.string
    ensure
      $stdout = stdout
    end
  end

  class UIImageModeTest < Minitest::Test
    def setup
      @ui = UI.allocate
      @ui.instance_variable_set(:@chafa_cmd, "/usr/bin/chafa")
      @ui.instance_variable_set(:@image_colors, "full")
    end

    def test_normalize_image_mode_defaults_to_compat
      assert_equal "compat", @ui.send(:normalize_image_mode, nil)
      assert_equal "compat", @ui.send(:normalize_image_mode, "stable")
    end

    def test_normalize_image_mode_accepts_balanced_and_high
      assert_equal "balanced", @ui.send(:normalize_image_mode, "balanced")
      assert_equal "high", @ui.send(:normalize_image_mode, "high")
    end

    def test_chafa_command_uses_ascii_mode_for_compat
      @ui.instance_variable_set(:@image_mode, "compat")

      command = @ui.send(:chafa_command, "/tmp/example.png", 40, 12)

      assert_includes command, "--format symbols"
      assert_includes command, "--symbols ascii"
      assert_includes command, "--colors none"
      assert_includes command, "--optimize 0"
      assert_includes command, "--work 1"
    end

    def test_chafa_command_uses_vhalf_mode_for_balanced
      @ui.instance_variable_set(:@image_mode, "balanced")

      command = @ui.send(:chafa_command, "/tmp/example.png", 40, 12)

      assert_includes command, "--symbols vhalf"
      assert_includes command, "--colors full"
      assert_includes command, "--optimize 5"
      assert_includes command, "--work 5"
    end

    def test_chafa_command_uses_denser_symbols_for_high
      @ui.instance_variable_set(:@image_mode, "high")

      command = @ui.send(:chafa_command, "/tmp/example.png", 40, 12)

      assert_includes command, "--symbols vhalf+block"
      assert_includes command, "--colors full"
      assert_includes command, "--optimize 9"
      assert_includes command, "--work 9"
    end

    def test_chafa_native_sixel_argv_uses_top_left_fullscreen_viewport
      argv = @ui.send(:chafa_native_sixel_argv, "/tmp/example.png", 80, 23)

      assert_includes argv, "--format"
      assert_includes argv, "sixels"
      assert_includes argv, "--scale"
      assert_includes argv, "max"
      assert_includes argv, "--align"
      assert_includes argv, "top,left"
      assert_includes argv, "--margin-bottom"
      assert_includes argv, "0"
      assert_includes argv, "--view-size"
      assert_includes argv, "80x23"
    end
  end

  class UISixelSupportTest < Minitest::Test
    def setup
      @ui = UI.allocate
    end

    def test_parse_da1_sixel_response_detects_sixel_extension
      assert_equal true, @ui.send(:parse_da1_sixel_response, "\e[?62;4;9;22c")
    end

    def test_parse_da1_sixel_response_rejects_responses_without_sixel_extension
      assert_equal false, @ui.send(:parse_da1_sixel_response, "\e[?1;2;6;9;15;18;22c")
    end

    def test_parse_pixel_response_reads_text_area_size
      assert_equal [1280, 720], @ui.send(:parse_pixel_response, "\e[4;720;1280t", expected_code: "4")
    end

    def test_parse_pixel_response_reads_cell_size
      assert_equal [9, 18], @ui.send(:parse_pixel_response, "\e[6;18;9t", expected_code: "6")
    end

    def test_parse_xtsmgraphics_geometry_response_reads_sixel_geometry
      assert_equal [1920, 1080], @ui.send(:parse_xtsmgraphics_geometry_response, "\e[?2;0;1920;1080S", expected_item: "2")
    end

    def test_parse_xtsmgraphics_geometry_response_rejects_unsuccessful_status
      assert_nil @ui.send(:parse_xtsmgraphics_geometry_response, "\e[?2;3;1920;1080S", expected_item: "2")
    end

    def test_fullscreen_native_pixel_viewport_reserves_footer_row
      @ui.define_singleton_method(:query_sixel_graphics_pixels) { [1280, 720] }
      @ui.define_singleton_method(:query_cell_pixels) { [9, 18] }

      assert_equal [1280, 702], @ui.send(:fullscreen_native_pixel_viewport, 40)
    end

    def test_render_fullscreen_image_native_writes_native_payload_and_footer
      ui = UI.allocate
      ui.instance_variable_set(:@renderer, UI::ScreenRenderer.new(pad_line: ->(line, width) { line.ljust(width) }))
      ui.instance_variable_set(:@locale, "en")
      ui.instance_variable_set(:@theme, UI::BUILTIN_THEMES["default"])
      ui.instance_variable_set(:@color_mode, "truecolor")
      ui.define_singleton_method(:render_native_image_url) { |_url, _width, _height, pixel_viewport: nil| "SIXELPAYLOAD" }
      ui.define_singleton_method(:fullscreen_native_pixel_viewport) { |_screen_height| [1280, 702] }
      output = with_stubbed_screen_size(80, 24) do
        capture_stdout { ui.send(:render_fullscreen_image_native, "https://example.com/test.png") }
      end

      assert_includes output, TTY::Cursor.clear_screen
      assert_includes output, "SIXELPAYLOAD"
      assert_includes output, "x/esc: back"
    end

    private

    def with_stubbed_screen_size(width, height)
      original_width = TTY::Screen.method(:width)
      original_height = TTY::Screen.method(:height)
      TTY::Screen.define_singleton_method(:width) { width }
      TTY::Screen.define_singleton_method(:height) { height }
      yield
    ensure
      TTY::Screen.define_singleton_method(:width, original_width)
      TTY::Screen.define_singleton_method(:height, original_height)
    end

    def capture_stdout
      stdout = $stdout
      fake = StringIO.new
      $stdout = fake
      yield
      fake.string
    ensure
      $stdout = stdout
    end
  end

  class UIReadKeypressWithTickTest < Minitest::Test
    class FakeInput
      def initialize(waitables)
        @waitables = waitables
      end

      def raw
        yield
      end

      def noecho
        yield
      end

      def wait_readable(_timeout)
        @waitables.shift
      end
    end

    class FakeReader
      attr_reader :input

      def initialize(waitables:, responses:)
        @input = FakeInput.new(waitables)
        @responses = responses
      end

      def read_keypress(**)
        @responses.shift
      end
    end

    def test_read_keypress_with_tick_returns_tick_on_timeout
      ui = UI.allocate
      ui.instance_variable_set(:@reader, FakeReader.new(waitables: [false], responses: []))
      ui.instance_variable_set(:@tick_seconds, 0.001)
      ui.instance_variable_set(:@resized, false)

      assert_equal :__tick__, ui.send(:read_keypress_with_tick)
    end

    def test_read_keypress_with_tick_returns_tick_when_resized
      ui = UI.allocate
      ui.instance_variable_set(:@reader, FakeReader.new(waitables: [], responses: []))
      ui.instance_variable_set(:@tick_seconds, 0.001)
      ui.instance_variable_set(:@resized, true)

      assert_equal :__tick__, ui.send(:read_keypress_with_tick)
    end

    def test_read_keypress_with_tick_assembles_escape_sequence
      ui = UI.allocate
      ui.instance_variable_set(
        :@reader,
        FakeReader.new(waitables: [true], responses: ["\e", "[", "B", nil])
      )
      ui.instance_variable_set(:@tick_seconds, 0.001)
      ui.instance_variable_set(:@resized, false)

      assert_equal "\e[B", ui.send(:read_keypress_with_tick)
    end
  end

  class UITopicListFormattingTest < Minitest::Test
    def setup
      @ui = UI.allocate
      @ui.instance_variable_set(:@theme, UI::BUILTIN_THEMES["default"])
      @ui.instance_variable_set(:@color_mode, "truecolor")
      @ui.instance_variable_set(:@api_username, "turnitaround")
      @ui.instance_variable_set(:@topic_list_users_by_id, {})
    end

    def test_themed_topic_list_line_ellipsizes_to_fit_width
      line = @ui.send(
        :themed_topic_list_line,
        1,
        "An extremely long topic title that should never wrap in compact mode",
        42,
        width: 36
      )

      visible = @ui.send(:strip_all_ansi, line)
      assert_operator @ui.send(:display_width, visible), :<=, 36
      assert_includes visible, "..."
    end

    def test_themed_topic_list_line_appends_unread_badge
      topic = { "unread_posts" => 3 }

      line = @ui.send(
        :themed_topic_list_line,
        1,
        "A topic with unread posts",
        42,
        width: 48,
        topic: topic
      )

      visible = @ui.send(:strip_all_ansi, line)
      assert_includes visible, "[3]"
      assert_operator @ui.send(:display_width, visible), :<=, 48
    end

    def test_themed_topic_list_line_with_badge_still_fits_requested_width
      topic = { "unread_posts" => 3 }

      line = @ui.send(
        :themed_topic_list_line,
        1,
        "A topic with unread posts",
        42,
        width: 24,
        topic: topic
      )

      visible = @ui.send(:strip_all_ansi, line)
      assert_operator @ui.send(:display_width, visible), :<=, 24
    end

    def test_themed_pm_topic_list_compact_line_ellipsizes_to_fit_width
      topic = {
        "title" => "ignored",
        "participants" => [
          { "username" => "turnitaround" },
          { "username" => "dizzydan" },
          { "username" => "maclunkey" }
        ],
        "reply_count" => 2
      }

      line = @ui.send(
        :themed_pm_topic_list_compact_line,
        4,
        "A very long PM title that should be shortened before it can wrap",
        topic,
        width: 42
      )

      visible = @ui.send(:strip_all_ansi, line)
      assert_operator @ui.send(:display_width, visible), :<=, 42
      assert_includes visible, "..."
      assert_includes visible, "(dizzydan, maclunkey)"
    end

    def test_topic_list_mode_uses_exact_thresholds
      assert_equal :compact, @ui.send(:topic_list_mode, UI::TOPIC_LIST_WIDE_CATEGORY_MIN - 1)
      assert_equal :category, @ui.send(:topic_list_mode, UI::TOPIC_LIST_WIDE_CATEGORY_MIN)
      assert_equal :category, @ui.send(:topic_list_mode, UI::TOPIC_LIST_WIDE_STATS_MIN - 1)
      assert_equal :stats, @ui.send(:topic_list_mode, UI::TOPIC_LIST_WIDE_STATS_MIN)
    end

    def test_themed_topic_list_row_appends_unseen_badge_to_title_cell
      topic = {
        "title" => "Fresh topic",
        "unseen" => true,
        "category_id" => 2,
        "reply_count" => 0
      }
      @ui.define_singleton_method(:site_categories) { { 2 => "Staff" } }

      line = @ui.send(:themed_topic_list_row, topic, 1, 130, :category, :latest)
      visible = @ui.send(:strip_all_ansi, line)

      assert_includes visible, "Fresh topic •"
    end

    def test_themed_topic_list_row_keeps_following_columns_themed_after_badge
      topic = {
        "title" => "Fresh topic",
        "unseen" => true,
        "category_id" => 2,
        "reply_count" => 0
      }
      @ui.define_singleton_method(:site_categories) { { 2 => "Staff" } }

      line = @ui.send(:themed_topic_list_row, topic, 1, 130, :category, :latest)
      list_text_ansi = @ui.send(:ansi_fg, @ui.send(:theme_color, "list_text"))

      assert_includes line, "#{list_text_ansi}Staff"
      assert_match(/#{Regexp.escape(list_text_ansi)}\s+0/, line)
    end

    def test_themed_topic_list_row_themes_inter_column_separators
      topic = {
        "title" => "Fresh topic",
        "unseen" => true,
        "category_id" => 2,
        "reply_count" => 0
      }
      @ui.define_singleton_method(:site_categories) { { 2 => "Staff" } }

      line = @ui.send(:themed_topic_list_row, topic, 1, 130, :category, :latest)
      list_text_ansi = @ui.send(:ansi_fg, @ui.send(:theme_color, "list_text"))

      assert_includes line, "#{list_text_ansi}  "
    end

    def test_fit_topic_list_cell_preserves_ansi_when_clipping_styled_text
      styled = "#{@ui.send(:theme_text, '•', fg: 'accent')}#{@ui.send(:theme_text, 'abcdef', fg: 'list_text')}"
      fitted = @ui.send(:fit_topic_list_cell, styled, 3, align: :left)
      accent_ansi = @ui.send(:ansi_fg, @ui.send(:theme_color, "accent"))

      assert_operator @ui.send(:display_width, @ui.send(:strip_all_ansi, fitted)), :<=, 3
      assert_includes fitted, accent_ansi
    end

    def test_login_label_appends_unread_notifications_badge
      @ui.instance_variable_set(:@notification_unread_count, 4)

      label = @ui.send(:login_label)

      assert_includes @ui.send(:strip_all_ansi, label), "Logged in: turnitaround [4]"
    end

    def test_build_header_line_visible_right_aligns_login_badge_without_losing_width
      @ui.instance_variable_set(:@notification_unread_count, 4)

      line = @ui.send(:build_header_line_visible, "Topic List: Latest", @ui.send(:login_label), 60)

      assert_equal 60, @ui.send(:visible_length, line)
      assert_includes @ui.send(:strip_all_ansi, line), "Logged in: turnitaround [4]"
    end

    def test_themed_notification_row_ellipsizes_to_fit_width
      @ui.define_singleton_method(:site_notification_types_by_id) { { 5 => "liked" } }
      notification = {
        "read" => false,
        "notification_type" => 5,
        "created_at" => (Time.now - 7200).iso8601,
        "fancy_title" => "A very long topic title that should be shortened cleanly in the notifications list",
        "data" => { "display_username" => "merefield" }
      }

      line = @ui.send(:themed_notification_row, notification, 48)
      visible = @ui.send(:strip_all_ansi, line)

      assert_operator @ui.send(:display_width, visible), :<=, 48
      assert_includes visible, "•"
      assert_includes visible, "2h"
    end

    def test_filter_notifications_by_likes
      @ui.define_singleton_method(:site_notification_types_by_id) { { 5 => "liked", 2 => "replied" } }
      notifications = [
        { "notification_type" => 5 },
        { "notification_type" => 2 }
      ]

      filtered = @ui.send(:filter_notifications, notifications, :likes)

      assert_equal [5], filtered.map { |notification| notification["notification_type"] }
    end

    def test_initial_topic_selected_index_accepts_selected_post_number
      posts = [
        { "id" => 10, "post_number" => 1 },
        { "id" => 11, "post_number" => 2 },
        { "id" => 12, "post_number" => 3 }
      ]

      selected = @ui.send(:initial_topic_selected_index, posts, {}, { post_number: 2 }, 123)

      assert_equal 1, selected
    end
  end

  class UINotificationStateTest < Minitest::Test
    FakeLiveUpdates = Struct.new(:unread_notification_count, :pm_unread_count) do
      def set_unread_notification_count(count)
        self.unread_notification_count = count
      end

      def set_pm_unread_count(count)
        self.pm_unread_count = count
      end

      def consume_resync_request
        false
      end

      def consume_topic_list_refresh_request
        false
      end
    end

    def setup
      @ui = UI.allocate
    end

    def test_refresh_notification_unread_count_preserves_existing_count_on_failed_refresh
      live_updates = FakeLiveUpdates.new(6, 2)
      @ui.instance_variable_set(:@live_updates, live_updates)
      @ui.instance_variable_set(:@notification_unread_count, 6)
      @ui.define_singleton_method(:with_errors) { nil }

      result = @ui.send(:refresh_notification_unread_count)

      assert_equal 6, @ui.instance_variable_get(:@notification_unread_count)
      assert_equal 6, live_updates.unread_notification_count
      assert_equal false, result[:notification]
    end

    def test_unread_notification_count_uses_live_zero_value
      @ui.instance_variable_set(:@live_updates, FakeLiveUpdates.new(0, 0))
      @ui.instance_variable_set(:@notification_unread_count, 6)

      assert_equal 0, @ui.send(:unread_notification_count)
    end

    def test_pm_unread_count_uses_live_zero_value
      @ui.instance_variable_set(:@live_updates, FakeLiveUpdates.new(6, 0))
      @ui.instance_variable_set(:@pm_unread_count, 4)

      assert_equal 0, @ui.send(:pm_unread_count)
    end

    def test_mark_notification_read_only_updates_local_state_after_success
      notification = { "id" => 8, "read" => false }
      @ui.define_singleton_method(:with_errors) { nil }

      @ui.send(:mark_notification_read, notification)

      assert_equal false, notification["read"]

      @ui.define_singleton_method(:with_errors) { {} }

      @ui.send(:mark_notification_read, notification)

      assert_equal true, notification["read"]
    end

    def test_notification_relative_time_uses_weeks_until_one_year
      value = (Time.now - (60 * 24 * 60 * 60)).iso8601

      assert_equal "8w", @ui.send(:notification_relative_time, value)
    end
  end

  class UISiteInfoCachingTest < Minitest::Test
    def setup
      @ui = UI.allocate
    end

    def test_site_info_retries_after_transient_failure
      responses = [nil, { "categories" => [{ "id" => 2, "name" => "Staff" }] }]
      @ui.define_singleton_method(:with_errors) { responses.shift }

      assert_equal({}, @ui.send(:site_info))
      assert_equal({ "categories" => [{ "id" => 2, "name" => "Staff" }] }, @ui.send(:site_info))
    end

    def test_site_categories_do_not_cache_empty_result_after_failed_site_info
      responses = [nil, { "categories" => [{ "id" => 2, "name" => "Staff" }] }]
      @ui.define_singleton_method(:with_errors) { responses.shift }

      assert_equal({}, @ui.send(:site_categories))
      assert_equal({ 2 => "Staff" }, @ui.send(:site_categories))
    end

    def test_notification_types_do_not_cache_empty_result_after_failed_site_info
      responses = [nil, { "notification_types" => { "liked" => 5 } }]
      @ui.define_singleton_method(:with_errors) { responses.shift }

      assert_equal({}, @ui.send(:site_notification_types_by_id))
      assert_equal({ 5 => "liked" }, @ui.send(:site_notification_types_by_id))
    end
  end

  class UISearchFormattingTest < Minitest::Test
    def setup
      @ui = UI.allocate
      @ui.instance_variable_set(:@display_url, "meta.discourse.org")
      @ui.instance_variable_set(:@pastel, Pastel.new(enabled: true))

      @ui.define_singleton_method(:build_header_line) { |_left, _right, _width| "HEADER" }
      @ui.define_singleton_method(:build_themed_header_box) { |content, _width| content.join("\n") }
      @ui.define_singleton_method(:emojify) { |text| text }
      @ui.define_singleton_method(:render_screen) do |lines, **kwargs|
        @_captured_render = { lines: lines, kwargs: kwargs }
      end
    end

    def test_render_search_results_keeps_rows_single_line_and_truncates
      posts = [
        {
          "topic_id" => 101,
          "blurb" => "<p>This chatbot result has a very long explanation that should be shortened to one visible line in the results pane.</p>"
        }
      ]
      topics_map = {
        101 => "Discourse Chatbot :robot: with a very long title that must be truncated"
      }

      with_tty_screen(width: 60, height: 10) do
        @ui.send(:render_search_results, "chatbot", posts, topics_map, 0)
      end

      captured = @ui.instance_variable_get(:@_captured_render)
      refute_nil captured

      result_line = captured[:lines][4]
      visible = @ui.send(:strip_all_ansi, result_line)

      refute_includes visible, "\n"
      assert_operator @ui.send(:display_width, visible), :<=, 59
      assert_includes visible, "..."
      assert_match(/\e\[1mchatbot\e\[0m/i, result_line)
    end

    private

    def with_tty_screen(width:, height:)
      screen = TTY::Screen.singleton_class
      original_width = TTY::Screen.method(:width)
      original_height = TTY::Screen.method(:height)

      screen.send(:define_method, :width) { width }
      screen.send(:define_method, :height) { height }
      yield
    ensure
      screen.send(:define_method, :width, original_width)
      screen.send(:define_method, :height, original_height)
    end
  end
end
