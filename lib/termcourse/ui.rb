# frozen_string_literal: true

require "cgi"
require "json"
require "time"
require "open-uri"
require "tempfile"
require "shellwords"
require "open3"
require "yaml"
require "tty-screen"
require "tty-cursor"
require "tty-box"
require "tty-reader"
require "tty-prompt"
require "tty-markdown"
require "pastel"

module Termcourse
  class UI
    TOPIC_LIST_WIDE_CATEGORY_MIN = 125
    TOPIC_LIST_WIDE_STATS_MIN = 149

    BUILTIN_THEMES = {
      "default" => {
        "primary" => "#f2f2f2",
        "background" => nil,
        "highlighted" => "#2a5ea8",
        "highlighted_text" => "#ffffff",
        "borders" => "#a8a8a8",
        "bar_backgrounds" => "#1f1f1f",
        "separators" => "#6f6f6f",
        "list_numbers" => "#f2f2f2",
        "list_text" => "#e6e6e6",
        "list_meta" => "#b5b5b5",
        "accent" => "#6cc4ff"
      },
      "slate" => {
        "primary" => "#e6edf3",
        "background" => nil,
        "highlighted" => "#355f8a",
        "highlighted_text" => "#ffffff",
        "borders" => "#5f6f80",
        "bar_backgrounds" => "#1f2733",
        "separators" => "#8ca0b3",
        "list_numbers" => "#8fbce6",
        "list_text" => "#dde7f0",
        "list_meta" => "#9ab0c6",
        "accent" => "#66c2ff"
      },
      "fairground" => {
        "primary" => "#f6fff5",
        "background" => nil,
        "highlighted" => "#0055aa",
        "highlighted_text" => "#ffffff",
        "borders" => "#1f8f5f",
        "bar_backgrounds" => "#103f33",
        "separators" => "#ff4b4b",
        "list_numbers" => "#4ecb71",
        "list_text" => "#d8f7e4",
        "list_meta" => "#8fd9ad",
        "accent" => "#2a9dff"
      },
      "rust" => {
        "primary" => "#efe2c4",
        "background" => nil,
        "highlighted" => "#b5521e",
        "highlighted_text" => "#fff7e8",
        "borders" => "#b06c2f",
        "bar_backgrounds" => "#3a2516",
        "separators" => "#d2b168",
        "list_numbers" => "#d58a3d",
        "list_text" => "#f2e4c8",
        "list_meta" => "#c9b287",
        "accent" => "#e0b85f"
      }
    }.freeze

    def initialize(base_url, api_key: nil, api_username: nil, client: nil, theme_name: nil)
      @client = client || Client.new(base_url, api_key: api_key, api_username: api_username)
      @reader = TTY::Reader.new
      @prompt = TTY::Prompt.new
      @pastel = Pastel.new
      @api_username = api_username
      @base_url = base_url
      @display_url = base_url.sub(%r{\Ahttps?://}i, "")
      @theme_name = (theme_name || ENV.fetch("TERMCOURSE_THEME", "default")).to_s.downcase
      @theme = load_theme(@theme_name)
      @links_enabled = ENV.fetch("TERMCOURSE_LINKS", "1") != "0"
      @emoji_enabled = ENV.fetch("TERMCOURSE_EMOJI", "1") != "0"
      @image_backend_preference = ENV.fetch("TERMCOURSE_IMAGE_BACKEND", "auto").to_s.downcase
      @image_mode = ENV.fetch("TERMCOURSE_IMAGE_MODE", "stable").to_s.downcase
      @image_mode = "stable" unless %w[stable quality].include?(@image_mode)
      @image_colors_preference = ENV.fetch("TERMCOURSE_IMAGE_COLORS", "auto").to_s.downcase
      @image_colors = resolve_image_colors
      @images_enabled = ENV.fetch("TERMCOURSE_IMAGES", "1") != "0"
      @image_debug_enabled = ENV.fetch("TERMCOURSE_IMAGE_DEBUG", "0") == "1"
      @image_lines = ENV.fetch("TERMCOURSE_IMAGE_LINES", "14").to_i
      @image_lines = 14 if @image_lines <= 0
      @image_quality_filter = ENV.fetch("TERMCOURSE_IMAGE_QUALITY_FILTER", "1") != "0"
      @image_max_bytes = ENV.fetch("TERMCOURSE_IMAGE_MAX_BYTES", "5242880").to_i
      @image_max_bytes = 5_242_880 if @image_max_bytes <= 0
      @tick_ms = ENV.fetch("TERMCOURSE_TICK_MS", "100").to_i
      @tick_ms = 100 if @tick_ms <= 0
      @tick_seconds = @tick_ms / 1000.0
      @resolved_execs = {}
      @viu_cmd = resolve_executable("viu")
      @chafa_cmd = resolve_executable("chafa")
      @image_backend = detect_image_backend
      @image_cache = {}
      @topic_list_users_by_id = {}
      @pm_unread_count = 0
      @last_read_post_sent = {}
      @debug_enabled = ENV.fetch("TERMCOURSE_DEBUG", "0") == "1"
      image_debug_log("init enabled=#{@images_enabled} backend_pref=#{@image_backend_preference} backend=#{@image_backend || 'none'} mode=#{@image_mode} colors=#{@image_colors}") if @image_debug_enabled
      @resized = false
      trap_resize
    end

    def run
      filter = :latest
      top_period = :monthly
      loop do
        refresh_pm_unread_count
        topics_data = fetch_list(filter, top_period)
        return if topics_data.nil?
        merge_topic_list_users(topics_data)

        topics = topics_data.dig("topic_list", "topics") || []
        next_url = topics_data.dig("topic_list", "more_topics_url")
        result = topic_list_loop(topics, next_url, filter, top_period)

        break if result == :quit
        if result.is_a?(Hash) && result[:filter]
          filter = result[:filter]
          next
        end
        if result.is_a?(Hash) && result[:top_period]
          top_period = result[:top_period]
          next
        end
        if result.is_a?(Hash) && result[:search]
          query = result[:search]
          loop do
            search_result = search_loop(query)
            break if search_result.nil?

            topic_result = topic_loop(
              search_result[:topic_id],
              search_result[:post_id],
              back_result: :back_to_search
            )
            break if topic_result == :quit
            next if topic_result == :back_to_search
          end
          next
        end
        if result.is_a?(Hash) && result[:new_topic]
          create_topic_from(result[:new_topic])
          next
        end
        next if result == :reload

        topic_result = topic_loop(result)
        break if topic_result == :quit
      end
    end

    private

    def topic_list_loop(topics, next_url, filter, top_period)
      selected = 0
      loading = false
      filters = %i[latest unread private hot new top]
      filter_index = filters.index(filter) || 0
      top_periods = %i[daily weekly monthly quarterly yearly]
      period_index = top_periods.index(top_period) || 2

      loop do
        render_topic_list(
          topics,
          selected,
          filter: filters[filter_index],
          top_period: top_periods[period_index],
          loading: loading
        )
        key = read_keypress_with_tick
        if key == :__tick__
          @resized = false
          next
        end
        if @resized
          @resized = false
          next
        end

        case key
        when "\u001b[A" # up
          selected = [selected - 1, 0].max
        when "\u001b[B" # down
          selected = [selected + 1, topics.length - 1].min
          if next_url && selected >= topics.length - 3 && !loading
            loading = true
            render_topic_list(
              topics,
              selected,
              filter: filters[filter_index],
              top_period: top_periods[period_index],
              loading: loading
            )
            more = fetch_more_topics(next_url)
            more_topics = more&.dig("topic_list", "topics") || []
            merge_topic_list_users(more)
            next_url = more&.dig("topic_list", "more_topics_url")
            topics.concat(more_topics)
            loading = false
          end
        when "\r", "\n" # enter
          topic = topics[selected]
          return topic["id"] if topic
        when "1", "2", "3", "4", "5", "6", "7", "8", "9", "0"
          index = (key == "0") ? 9 : (key.to_i - 1)
          topic = topics[index]
          return topic["id"] if topic
        when "f"
          filter_index = (filter_index + 1) % filters.length
          return { filter: filters[filter_index] }
        when "p"
          next unless filters[filter_index] == :top

          period_index = (period_index + 1) % top_periods.length
          return { top_period: top_periods[period_index] }
        when "s"
          query = prompt_search_query
          return { search: query } if query
        when "n"
          new_topic = new_topic_flow
          return { new_topic: new_topic } if new_topic
        when "g"
          return :reload
        when "q", "\u001b"
          return :quit
        end
      end
    end

    def topic_loop(topic_id, selected_post_id = nil, back_result: nil)
      topic_data = fetch_topic(topic_id)
      return if topic_data.nil?

      posts = topic_data.dig("post_stream", "posts") || []
      selected = 0
      if selected_post_id
        idx = posts.find_index { |p| p["id"] == selected_post_id }
        selected = idx if idx
      end
      scroll_offsets = Hash.new(0)

      loop do
        maybe_update_read_state(topic_id, posts[selected])
        render_topic(topic_data, posts, selected, scroll_offsets)
        key = read_keypress_with_tick
        if key == :__tick__
          @resized = false
          next
        end
        if @resized
          @resized = false
          next
        end

        case key
        when "\u001b[A" # up
          selected = [selected - 1, 0].max
        when "\u001b[B" # down
          selected = [selected + 1, posts.length - 1].min
        when "\u001b[C" # right
          scroll_offsets[selected] += 3
        when "\u001b[D" # left
          scroll_offsets[selected] = [scroll_offsets[selected] - 3, 0].max
        when "s"
          query = prompt_search_query
          search_result = search_loop(query) if query
          if search_result
            topic_id = search_result[:topic_id]
            topic_data = fetch_topic(search_result[:topic_id])
            posts = topic_data.dig("post_stream", "posts") || []
            selected = posts.find_index { |p| p["id"] == search_result[:post_id] } || 0
            scroll_offsets = Hash.new(0)
          end
        when "l"
          post = posts[selected]
          next unless post

          toggle_like(post)
          topic_data = fetch_topic(topic_id)
          posts = topic_data.dig("post_stream", "posts") || []
          selected = [selected, posts.length - 1].min
        when "r"
          if reply_to_topic(topic_id)
            topic_data = fetch_topic(topic_id)
            posts = topic_data.dig("post_stream", "posts") || []
            scroll_offsets = Hash.new(0)
          end
        when "p"
          post = posts[selected]
          if post && reply_to_post(topic_id, post)
            topic_data = fetch_topic(topic_id)
            posts = topic_data.dig("post_stream", "posts") || []
            scroll_offsets = Hash.new(0)
          end
        when "x"
          post = posts[selected]
          image_url = image_expand_url_for_post(post, TTY::Screen.width)
          fullscreen_image_loop(image_url) if image_url
        when "q"
          return :quit
        when "\u001b", "\u007f"
          return back_result
        end
      end
    end

    def render_topic_list(topics, selected, filter:, top_period:, loading: false)
      clear_screen
      width = TTY::Screen.width
      height = TTY::Screen.height
      list_mode = topic_list_mode(width)

      controls = "arrows: move | ↵: open | 1-0: open top10 | n: new | s: search | f: filter"
      controls += " | p: period" if filter == :top
      controls += " | g: refresh | q: quit"

      top_line = build_header_line(controls, @display_url, width - 4)
      status_label = "Topic List: #{topic_list_filter_label(filter)}"
      status_label += " (#{top_period.to_s.capitalize})" if filter == :top
      status = loading ? "#{status_label} | Loading more..." : status_label
      right_status = topic_list_right_status
      content = [
        top_line,
        "-" * (width - 4),
        build_header_line(status, right_status, width - 4)
      ]
      header_height = render_header(content, width)

      if topics.empty?
        puts "No topics found."
        return
      end

      max_lines = [height - header_height - 1, 1].max
      if list_mode != :compact
        puts themed_topic_list_header(width, list_mode, filter)
        max_lines = [max_lines - 1, 1].max
      end
      start_index = [selected - (max_lines / 2), 0].max
      end_index = [start_index + max_lines - 1, topics.length - 1].min

      topics[start_index..end_index].each_with_index do |topic, idx|
        line_index = start_index + idx
        line = if list_mode == :compact
                 title = truncate(topic["title"].to_s, width - 10)
                 if filter == :private
                   themed_pm_topic_list_compact_line(line_index + 1, title, topic, width: width)
                 else
                   replies = topic_reply_count(topic)
                   themed_topic_list_line(line_index + 1, title, replies, width: width)
                 end
               else
                 themed_topic_list_row(topic, line_index + 1, width, list_mode, filter)
               end
        line = theme_highlight(strip_all_ansi(line)) if line_index == selected
        puts line
      end
    end

    def render_topic(topic_data, posts, selected, scroll_offsets)
      width = TTY::Screen.width
      height = TTY::Screen.height
      title = topic_data["title"].to_s
      selected_post = posts[selected]
      image_expand_url = image_expand_url_for_post(selected_post, width)

      controls = "arrows: move | l: like | r: reply topic | p: reply post | s: search | esc: back | q: quit"
      controls += " | x: image" if image_expand_url
      top_line = build_header_line(controls, @display_url, width - 4)
      topic_line = "Topic: #{truncate(title, width - 4)}"
      category_label = category_label_from_topic_data(topic_data)
      header_lines = [
        top_line,
        "-" * (width - 4),
        build_header_line(topic_line, category_label, width - 4)
      ]
      header_box = build_themed_header_box(header_lines, width)
      header_lines_rendered = header_box.split("\n")
      header_height = header_lines_rendered.length

      footer_box = build_progress_footer(posts.length, selected, width)
      footer_lines = footer_box.split("\n")
      footer_height = footer_lines.length

      list_height_lines = [height - header_height - footer_height, 1].max
      list_lines = build_post_list_lines(posts, selected, scroll_offsets[selected], list_height_lines, width)
      list_lines = list_lines.first(list_height_lines)
      list_lines.fill("", list_lines.length...list_height_lines)
      debug_selected_post(posts, selected, width, height) if @debug_enabled

      screen = Array.new(height) { " " * width }
      header_lines_rendered.each_with_index do |line, idx|
        screen[idx] = pad_line(line, width)
      end

      list_start = header_height
      list_lines.each_with_index do |line, idx|
        row = list_start + idx
        break if row >= height - footer_height
        screen[row] = pad_line(line, width)
      end

      footer_start = height - footer_height
      footer_lines.each_with_index do |line, idx|
        row = footer_start + idx
        break if row >= height
        screen[row] = pad_line(line, width)
      end

      clear_screen
      print screen.join("\n")
    end

    def debug_selected_post(posts, selected, width, height)
      post = posts[selected]
      return if post.nil?

      lines = build_post_block(post, true, width)
      File.open("/tmp/termcourse_debug.txt", "a") do |f|
        f.puts("---")
        f.puts("time=#{Time.now.utc.iso8601}")
        f.puts("screen=#{width}x#{height} selected=#{selected} post_id=#{post["id"]}")
        f.puts("raw=#{post["raw"].to_s.inspect}")
        lines.each_with_index do |line, idx|
          f.puts("line#{idx}: visible=#{visible_length(line)} bytes=#{line.bytesize} text=#{strip_all_ansi(line).inspect}")
        end
      end
    rescue StandardError
      nil
    end

    def build_post_list_lines(posts, selected, scroll_offset, list_height_lines, width)
      return ["No posts."] if posts.empty?

      blocks = posts.map.with_index do |post, index|
        build_post_block(post, index == selected, width)
      end
      block_lengths = blocks.map { |lines| lines.length + 1 }

      selected_max = [(list_height_lines * 0.6).to_i, 6].max
      selected_block = blocks[selected] || []
      selected_max = [selected_max, selected_block.length].min
      max_scroll = [selected_block.length - selected_max, 0].max
      scroll_offset = [[scroll_offset, 0].max, max_scroll].min
      selected_block = selected_block[scroll_offset, selected_max] || []
      show_up = scroll_offset.positive?
      show_down = scroll_offset < max_scroll

      remaining_lines = list_height_lines - (selected_block.length + 1)
      remaining_lines = 0 if remaining_lines.negative?

      rendered = []
      rendered << { index: selected, lines: decorate_scroll(selected_block, show_up, show_down, width) }

      offset = 1
      while remaining_lines > 0 && (selected - offset >= 0 || selected + offset < blocks.length)
        if selected - offset >= 0
          block = blocks[selected - offset]
          if block_lengths[selected - offset] <= remaining_lines
            rendered.unshift({ index: selected - offset, lines: block })
            remaining_lines -= block_lengths[selected - offset]
          else
            rendered.unshift({ index: selected - offset, lines: block.last([remaining_lines - 1, 0].max) })
            remaining_lines = 0
          end
        end

        break if remaining_lines <= 0

        if selected + offset < blocks.length
          block = blocks[selected + offset]
          if block_lengths[selected + offset] <= remaining_lines
            rendered << { index: selected + offset, lines: block }
            remaining_lines -= block_lengths[selected + offset]
          else
            rendered << { index: selected + offset, lines: block.first([remaining_lines - 1, 0].max) }
            remaining_lines = 0
          end
        end

        offset += 1
      end

      lines = []
      rendered.each_with_index do |item, idx|
        lines.concat(item[:lines])
        lines << theme_text("-" * width, fg: "separators") if idx != rendered.length - 1
      end
      lines = ["No posts."] if lines.empty?
      lines
    end

    def build_post_block(post, expanded, width)
      liked = post_liked?(post)
      liked_marker = ""
      username = post["username"].to_s
      heart = liked ? "❤️" : "🤍"
      header = "#{liked_marker}@#{username}"

      body_width = content_width(width)
      raw = post["raw"].to_s
      has_images = !extract_image_urls(raw).empty?
      image_debug_log("post id=#{post['id']} expanded=#{expanded} has_images=#{has_images} body_width=#{body_width}") if @image_debug_enabled
      image_lines = expanded ? image_preview_lines(raw, body_width) : []
      image_lines = left_align_image_preview_lines(image_lines) unless image_lines.empty?
      text_raw = has_images ? strip_markdown_images(raw) : raw
      lines = parse_markdown_lines(text_raw, body_width)
      content_lines = wrap_and_linkify_lines(lines, body_width)
      if expanded && !image_lines.empty?
        note = theme_text("x: expand image", fg: "list_meta")
        content_lines = image_lines + [note] + content_lines
      elsif has_images
        content_lines = [format_line("[image]", body_width)] + content_lines
      end

      if expanded
        header_line = pad_line(format_line(header, width, heart), width)
        header_line = highlight(header_line)
        [header_line] + content_lines
      else
        preview = content_lines.first(3)
        preview = [""] if preview.empty?
        [format_line(header, width, heart)] + preview
      end
    end

    def left_align_image_preview_lines(lines)
      lines.map do |line|
        # Keep ANSI style prefix, but remove plain-space padding around image content.
        trimmed = line.to_s
        trimmed = trimmed.gsub(/\A((?:\e\[[0-9;]*m)*) +/, '\1')
        trimmed = trimmed.gsub(/ +((?:\e\[[0-9;]*m)*)\z/, '\1')
        trimmed
      end
    end

    def decorate_scroll(lines, show_up, show_down, width)
      return lines if lines.empty?

      top = show_up ? "^^^ more above ^^^" : nil
      bottom = show_down ? "vvv more below vvv" : nil

      output = lines.dup
      if top
        output[0] = format_line(top, width)
      end
      if bottom
        output[-1] = format_line(bottom, width)
      end
      output
    end

    def parse_markdown_lines(raw, width)
      content =
        begin
          TTY::Markdown.parse(raw.to_s, width: width)
        rescue StandardError
          fallback_markdown_text(raw.to_s)
        end
      content = content.gsub("\r", "")
      content = content.gsub("\t", "  ")
      content = content.tr("\u2028\u2029\u0085", "   ")
      content = strip_invisible(content)
      content = strip_control_chars(content)
      content = strip_ansi(content)
      content = strip_ansi_residue(content)
      lines = content.split("\n").map { |line| emojify(line) }
      lines = [""] if lines.empty?
      lines
    end

    def fallback_markdown_text(text)
      out = text.dup
      # Guard against malformed HTML anchors that can crash tty-markdown URI parsing.
      out = out.gsub(/<a\b[^>]*>/i, "")
      out = out.gsub(%r{</a>}i, "")
      # Keep visible link text; drop link target when parsing failed.
      out = out.gsub(/\[([^\]]+)\]\(([^)]+)\)/, '\1')
      out
    end

    def wrap_and_linkify_lines(lines, width)
      lines.flat_map { |line| wrap_and_linkify_line(line, width) }
    end

    def wrap_and_linkify_line(line, width)
      url_regex = %r{https?://[^\s\)\]\}>,]+}
      segments = []
      index = 0

      while index < line.length
        match = line.match(url_regex, index)
        if match && match.begin(0) == index
          segments << { type: :url, text: match[0] }
          index = match.end(0)
        else
          next_index = match ? match.begin(0) : line.length
          segments << { type: :text, text: line[index...next_index] }
          index = next_index
        end
      end

      output = []
      current = +""
      current_len = 0

      flush = lambda do
        output << current unless current.empty?
        current = +""
        current_len = 0
      end

      append = lambda do |chunk, length|
        if current_len + length <= width
          current << chunk
          current_len += length
        else
          flush.call if current_len.positive?
          if length > width
            chunk_chars = chunk
            while chunk_chars.length.positive?
              take = [width, chunk_chars.length].min
              output << chunk_chars[0, take]
              chunk_chars = chunk_chars[take..]
            end
          else
            current << chunk
            current_len = length
          end
        end
      end

      segments.each do |segment|
        if segment[:type] == :text
          tokens = segment[:text].scan(/\S+|\s+/)
          tokens.each do |token|
            if token.strip.empty?
              next if current_len.zero?
              token_width = display_width(token)
              if current_len + token_width <= width
                append.call(token, token_width)
              else
                flush.call
              end
              next
            end

            token_width = display_width(token)
            if token_width > width
              flush.call if current_len.positive?
              token_chars = token
              while token_chars.length.positive?
                piece, rest = take_by_display_width(token_chars, width)
                append.call(piece, display_width(piece))
                token_chars = rest
                flush.call if current_len == width
              end
              next
            end

            if current_len + token_width > width
              flush.call
            end
            append.call(token, token_width)
          end
        else
          url = segment[:text]
          display = CGI.unescape(url)
          display_width_total = display_width(display)
          flush.call if current_len.positive? && display_width_total > (width - current_len)

          if display_width_total <= width
            append.call(osc8(url, display), display_width_total)
          else
            while display.length.positive?
              piece, rest = take_by_display_width(display, width)
              display = rest
              append.call(osc8(url, piece), display_width(piece))
              flush.call if current_len == width
            end
          end
        end
      end

      flush.call if current_len.positive?
      output = [""] if output.empty?
      output
    end

    def detect_image_backend
      return nil unless @images_enabled

      backends =
        case @image_backend_preference
        when "off", "none", "0" then []
        when "viu" then [:viu]
        when "chafa" then [:chafa]
        else [:chafa, :viu]
        end
      backends.find { |backend| backend_available?(backend) }
    end

    def resolve_image_colors
      allowed = %w[none 16 240 256 full]
      return @image_colors_preference if allowed.include?(@image_colors_preference)

      detect_terminal_image_colors
    end

    def detect_terminal_image_colors
      return "none" if ENV["NO_COLOR"]

      colorterm = ENV["COLORTERM"].to_s.downcase
      term = ENV["TERM"].to_s.downcase
      term_program = ENV["TERM_PROGRAM"].to_s.downcase

      return "full" if colorterm.include?("truecolor") || colorterm.include?("24bit")
      return "full" if ENV["WT_SESSION"]
      return "full" if term_program.include?("wezterm")
      return "full" if term.include?("direct")
      return "256" if term.include?("256color")
      return "none" if term == "dumb"

      "16"
    end

    def command_exists?(cmd)
      !resolve_executable(cmd).nil?
    end

    def resolve_executable(cmd)
      return @resolved_execs[cmd] if @resolved_execs.key?(cmd)

      path_entries = ENV.fetch("PATH", "").split(File::PATH_SEPARATOR)
      cargo_bin = File.expand_path("~/.cargo/bin")
      path_entries << cargo_bin unless path_entries.include?(cargo_bin)
      ext_candidates =
        if Gem.win_platform?
          ENV.fetch("PATHEXT", ".EXE;.BAT;.CMD").split(";")
        else
          [""]
        end

      path_entries.each do |dir|
        next if dir.to_s.strip.empty?

        ext_candidates.each do |ext|
          candidate = File.join(dir, "#{cmd}#{ext}")
          if File.file?(candidate) && File.executable?(candidate)
            @resolved_execs[cmd] = candidate
            return candidate
          end
        end
      end

      @resolved_execs[cmd] = nil
      nil
    end

    def backend_available?(backend)
      case backend
      when :viu then !@viu_cmd.nil?
      when :chafa then !@chafa_cmd.nil?
      else false
      end
    end

    def image_preview_lines(raw, width)
      return [] unless @images_enabled
      return [] if @image_backend.nil?

      urls = extract_image_urls(raw)
      image_debug_log("preview urls=#{urls.length} backend=#{@image_backend} width=#{width}")
      return [] if urls.empty?

      max_lines = @image_lines
      cache_key = [urls.first, width, max_lines, @image_backend]
      cached = @image_cache[cache_key]
      image_debug_log("preview cache_hit url=#{urls.first}") if cached
      return cached if cached

      rendered = render_image_url(urls.first, width, max_lines)
      image_debug_log("preview rendered_lines=#{rendered.length} url=#{urls.first}")
      @image_cache[cache_key] = rendered
      rendered
    rescue StandardError
      image_debug_log("preview exception")
      []
    end

    def image_expand_url_for_post(post, width)
      return nil unless @images_enabled
      return nil if @image_backend.nil?
      return nil if post.nil?

      raw = post["raw"].to_s
      urls = extract_image_urls(raw)
      return nil if urls.empty?
      return nil if image_preview_lines(raw, content_width(width)).empty?

      urls.first
    end

    def fullscreen_image_loop(url)
      return if url.nil?

      loop do
        render_fullscreen_image(url)
        key = read_keypress_with_tick
        if key == :__tick__
          @resized = false
          next
        end
        return if key == "x" || key == "\u001b"
      end
    end

    def render_fullscreen_image(url)
      clear_screen
      width = TTY::Screen.width
      height = TTY::Screen.height
      image_height = [height - 1, 1].max
      lines = render_image_url(url, width, image_height)
      lines = strip_image_header(lines)
      lines = fit_fullscreen_image_lines(url, width, image_height, lines)
      lines = lines.first(image_height).map { |line| center_image_line(line, width) }
      lines.fill("", lines.length...image_height)
      puts lines
      puts theme_text("x/esc: back", fg: "list_meta")
    end

    def center_image_line(line, width)
      trimmed = trim_image_line(line.to_s)
      trimmed = truncate_visible_with_ansi(trimmed, width) if visible_length(trimmed) > width
      visible = visible_length(trimmed)
      left_pad = [((width - visible) / 2), 0].max
      (" " * left_pad) + trimmed
    end

    def strip_image_header(lines)
      return [] if lines.nil? || lines.empty?

      lines.first&.strip == "[image]" ? (lines[1..] || []) : lines
    end

    def fit_fullscreen_image_lines(url, width, max_height, initial_lines)
      lines = initial_lines.first(max_height)
      attempts = 0
      while attempts < 3
        current_width = lines.map { |line| visible_length(trim_image_line(line.to_s)) }.max.to_i
        break if current_width <= width

        scale = width.to_f / current_width.to_f
        target_height = [(lines.length * scale).floor, 1].max
        break if target_height >= lines.length

        rerendered = strip_image_header(render_image_url(url, width, target_height))
        break if rerendered.empty?

        lines = rerendered.first(target_height)
        attempts += 1
      end
      lines
    end

    def trim_image_line(line)
      had_reset = line.end_with?("\e[0m")
      text = line.sub(/\e\[0m\z/, "")
      text = text.rstrip
      text << "\e[0m" if had_reset
      text
    end

    def extract_image_urls(raw)
      text = raw.to_s
      urls = []
      text.scan(/!\[[^\]]*\]\(([^)]+)\)/i) do |m|
        candidate = m[0].to_s.strip
        next if candidate.empty?

        if candidate.start_with?("upload://")
          urls << discourse_upload_url(candidate)
        elsif candidate.start_with?("http://", "https://")
          urls << candidate
        elsif candidate.start_with?("/uploads/")
          urls << "#{@base_url}#{candidate}"
        end
      end
      text.scan(/upload:\/\/[^\s\)]+/i) { |m| urls << discourse_upload_url(m) }
      text.scan(%r{/uploads/[^\s\)]+}i) { |m| urls << "#{@base_url}#{m}" }
      text.scan(%r{https?://[^\s\)]+}i) do |url|
        urls << url if url.match?(/\.(png|jpe?g|gif|webp|bmp)(\?.*)?$/i)
      end
      urls
        .map { |u| u.to_s.gsub(/[>"']+\z/, "") }
        .select { |u| u.match?(/\.(png|jpe?g|gif|webp|bmp)(\?.*)?$/i) }
        .uniq
    end

    def strip_markdown_images(raw)
      raw.to_s.gsub(/!\[[^\]]*\]\(([^)]+)\)/i, "")
    end

    def discourse_upload_url(upload_uri)
      path = upload_uri.to_s.sub(/\Aupload:\/\//, "")
      "#{@base_url}/uploads/short-url/#{path}"
    end

    def render_image_url(url, width, max_lines)
      image_debug_log("render start backend=#{@image_backend} mode=#{@image_mode} width=#{width} lines=#{max_lines} url=#{url}")
      uri = URI.parse(url)
      ext = File.extname(uri.path)
      Tempfile.create(["termcourse-image", ext]) do |tmp|
        download_image_with_limit(url, tmp, @image_max_bytes)
        tmp.flush
        image_debug_log("render downloaded bytes=#{tmp.size} path=#{tmp.path}")

        lines = render_with_backend(@image_backend, tmp.path, width, max_lines)
        image_debug_log("render raw_lines=#{lines.length} backend=#{@image_backend}")
        lines = sanitize_rendered_lines(lines, width, @image_backend)
        image_debug_log("render sanitized_lines=#{lines.length} backend=#{@image_backend}")
        if lines.empty? && @image_backend == :viu && backend_available?(:chafa)
          image_debug_log("render viu_empty fallback=chafa")
          fallback = render_with_backend(:chafa, tmp.path, width, max_lines)
          image_debug_log("render fallback_raw_lines=#{fallback.length}")
          lines = sanitize_rendered_lines(fallback, width, :chafa)
          image_debug_log("render fallback_sanitized_lines=#{lines.length}")
        end
        return [] if lines.empty?
        skip_quality_filter = (@image_backend == :viu) || (@image_backend == :chafa && @image_mode == "quality")
        return [] if @image_quality_filter && !skip_quality_filter && low_quality_image_preview?(lines)

        [format_line("[image]", width)] + lines
      end
    rescue StandardError
      image_debug_log("render exception")
      []
    end

    def download_image_with_limit(url, file, max_bytes)
      body = nil
      begin
        body = @client.get_bytes(url, max_bytes: max_bytes)
      rescue StandardError
        body = nil
      end

      if body.nil?
        bytes = 0
        URI.open(url, read_timeout: 8, open_timeout: 4) do |io|
          while (chunk = io.read(16_384))
            bytes += chunk.bytesize
            raise "image too large" if bytes > max_bytes

            file.write(chunk)
          end
        end
      else
        file.write(body)
      end
    end

    def render_with_chafa(path, width, max_lines)
      cmd = chafa_command(path, width, max_lines)
      `#{cmd}`.split("\n")
    end

    def render_with_viu(path, width, max_lines)
      viu_bin = @viu_cmd || "viu"
      shell_prefix = (@image_mode == "quality" && @image_colors == "full") ? "COLORTERM=truecolor " : ""
      argv = viu_argv(viu_bin, path, max_lines)
      shell_cmd = "#{shell_prefix}#{argv.map { |a| Shellwords.escape(a) }.join(' ')} 2>/dev/null"
      image_debug_log("viu shell cmd=#{shell_cmd}")
      shell_lines = `#{shell_cmd}`.split("\n")
      image_debug_log("viu shell lines=#{shell_lines.length}")
      return shell_lines if shell_lines.any? { |line| !line.strip.empty? }

      attempts = [{}, { "TERM" => "xterm-256color", "COLORTERM" => "falsecolor" }]
      attempts[0]["COLORTERM"] = "truecolor" if @image_mode == "quality" && @image_colors == "full"

      attempts.each do |env|
        stdout, _status = Open3.capture2e(env, *argv)
        lines = stdout.to_s.split("\n")
        image_debug_log("viu argv=#{argv.join(' ')} lines=#{lines.length} env_term=#{env['TERM'] || 'default'}")
        return lines if lines.any? { |line| !line.strip.empty? }
      end

      []
    end

    def chafa_command(path, width, max_lines)
      chafa_bin = Shellwords.escape(@chafa_cmd || "chafa")
      image = Shellwords.escape(path)
      if @image_mode == "quality"
        "#{chafa_bin} --format symbols --symbols vhalf --colors #{@image_colors} --size #{width}x#{max_lines} #{image} 2>/dev/null"
      else
        "#{chafa_bin} --format symbols --symbols ascii --colors none --size #{width}x#{max_lines} #{image} 2>/dev/null"
      end
    end

    def viu_argv(viu_bin, path, max_lines)
      [viu_bin, "-h", max_lines.to_s, "--blocks", "--transparent", path.to_s]
    end

    def render_with_backend(backend, path, width, max_lines)
      case backend
      when :chafa then render_with_chafa(path, width, max_lines)
      when :viu then render_with_viu(path, width, max_lines)
      else []
      end
    end

    def image_debug_log(message)
      return unless @image_debug_enabled

      File.open("/tmp/termcourse_image_debug.txt", "a") do |f|
        f.puts "[#{Time.now.utc.iso8601}] #{message}"
      end
    rescue StandardError
      nil
    end

    def sanitize_rendered_lines(lines, width, backend = nil)
      preserve_sgr = backend == :viu || (backend == :chafa && @image_mode == "quality")
      cleaned = lines
        .map do |line|
          clean = line.to_s.gsub(/[\r\n]/, "")
          clean = if preserve_sgr
                    keep_sgr_only(clean)
                  else
                    strip_all_ansi(clean)
                  end
          clean = if preserve_sgr
                    strip_controls_except_ansi(clean)
                  else
                    strip_control_chars(clean)
                  end
          clean = strip_invisible(clean)
          clean
        end
        .reject(&:empty?)
      if preserve_sgr
        visible = cleaned.map { |line| strip_all_ansi(line).strip }.reject(&:empty?)
        return [] if visible.empty?
        return cleaned
      end

      cleaned.map { |line| clamp_visible(line, width) }
    end

    def keep_sgr_only(text)
      out = text.dup
      out = out.gsub(/\e\]8;;.*?\a/, "")
      out = out.gsub(/\e\]8;;\a/, "")
      out = out.gsub(/\e_G.*?\e\\/, "")
      out = out.gsub(/\e\[(?![0-9;]*m)[0-9;?]*[ -\/]*[@-~]/, "")
      out
    end

    def strip_controls_except_ansi(text)
      text.gsub(/[\u0000-\u0008\u000B\u000C\u000E-\u001A\u001C-\u001F\u007F]/, "")
    end

    def low_quality_image_preview?(lines)
      text = lines.join
      return true if text.empty?

      # Reject previews that are mostly repeated block glyphs/noise.
      block_chars = text.scan(/[█▀▄▌▐▍▎▏▁▂▃▅▆▇░▒▓]/).length
      ratio = block_chars.to_f / text.length
      unique = text.chars.uniq.length
      ratio > 0.55 && unique <= 8
    end

    def osc8(url, text)
      return text unless @links_enabled

      "\e]8;;#{url}\a#{text}\e]8;;\a"
    end

    def emojify(text)
      return text unless @emoji_enabled

      text
        .gsub(":)", "🙂")
        .gsub(":-)", "🙂")
        .gsub(":(", "🙁")
        .gsub(":-(", "🙁")
        .gsub(";)", "😉")
        .gsub(";-)", "😉")
        .gsub(":D", "😄")
        .gsub(":-D", "😄")
        .gsub(":P", "😛")
        .gsub(":-P", "😛")
        .gsub(":heart:", "❤️")
        .gsub(":pizza:", "🍕")
        .gsub(":smile:", "😄")
        .gsub(":thumbsup:", "👍")
        .gsub(":fire:", "🔥")
        .gsub(":star:", "⭐")
        .gsub(":robot:", "🤖")
        .gsub(":robot_face:", "🤖")
    end

    def highlight(line)
      "\e[7m#{line}\e[0m"
    end

    def build_header_line(left, right, width)
      left = left.to_s
      right = right.to_s
      return truncate(left, width) if right.empty?
      return truncate(right, width) if right.length >= width

      left_width = width - right.length
      left_text = truncate(left, left_width).ljust(left_width)
      "#{left_text}#{right}"
    end

    def build_header_line_visible(left, right, width)
      left = left.to_s
      right = right.to_s
      return truncate_visible_with_ansi(left, width) if right.empty?

      right_text = truncate_visible_with_ansi(right, width)
      right_width = visible_length(right_text)
      return right_text if right_width >= width

      left_width = width - right_width
      left_text = truncate_visible_with_ansi(left, left_width)
      padding = [left_width - visible_length(left_text), 0].max
      "#{left_text}#{' ' * padding}#{right_text}"
    end

    def login_label
      username = @api_username.to_s
      username = "unknown" if username.strip.empty?
      "Logged in: #{username}"
    end

    def topic_list_right_status
      parts = []
      parts << pm_unread_label
      parts << login_label
      parts.compact.join(" | ")
    end

    def pm_unread_label
      count = @pm_unread_count.to_i
      return nil if count <= 0

      "PM Unread (#{count})"
    end

    def render_header(lines, width)
      header = build_themed_header_box(lines, width)
      print header.chomp
      print "\n"
      header.split("\n").length + 1
    end

    def build_themed_header_box(lines, width)
      themed_lines = theme_header_content(lines)
      box = build_header_box(themed_lines, width)
      theme_box_borders(box)
    end

    def build_header_box(lines, width)
      content = lines.join("\n")
      height = lines.length + 2
      TTY::Box.frame(width: width, height: height, padding: [0, 1]) { content }
    end

    def pad_line(text, width)
      line = text.to_s.gsub(/[\r\n]/, " ")
      line = strip_invisible(line)
      visible = visible_length(line)
      if visible > width
        line = clamp_visible(strip_all_ansi(line), width)
        visible = visible_length(line)
      end
      pad = [width - visible, 0].max
      line + (" " * pad)
    end

    def visible_length(text)
      stripped = text.gsub(/\e\[[0-9;]*m/, "")
      stripped = stripped.gsub(/\e\]8;;.*?\a/, "")
      stripped = stripped.gsub(/\e\]8;;\a/, "")
      display_width(stripped)
    end

    def strip_ansi(text)
      text.gsub(/\e\[[0-9;]*m/, "")
    end

    def strip_ansi_residue(text)
      text.gsub(/\[(?:\d{1,3}(?:;\d{1,3})*)m/, "")
    end

    def truncate_visible_with_ansi(text, max_width)
      return "" if max_width <= 0

      out = +""
      width = 0
      i = 0
      while i < text.length
        ch = text[i]
        if ch == "\e"
          if text[i + 1] == "["
            m_idx = text.index("m", i + 2)
            if m_idx
              out << text[i..m_idx]
              i = m_idx + 1
              next
            end
          elsif text[i + 1] == "]"
            a_idx = text.index("\a", i + 2)
            if a_idx
              out << text[i..a_idx]
              i = a_idx + 1
              next
            end
          end
        end

        ch_width = display_width(ch)
        break if width + ch_width > max_width

        out << ch
        width += ch_width
        i += 1
      end
      out
    end

    def strip_control_chars(text)
      text.gsub(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/, "")
    end

    def strip_invisible(text)
      text.gsub(/[\u200B-\u200F\u202A-\u202E\u2066-\u2069\uFEFF]/, "")
    end

    def strip_all_ansi(text)
      stripped = text.gsub(/\e\[[0-9;]*m/, "")
      stripped = stripped.gsub(/\e\]8;;.*?\a/, "")
      stripped = stripped.gsub(/\e\]8;;\a/, "")
      stripped
    end

    def clamp_visible(text, max_width)
      return "" if max_width <= 0

      width = 0
      out = +""
      text.each_char do |ch|
        ch_width = display_width(ch)
        break if width + ch_width > max_width

        out << ch
        width += ch_width
      end
      out
    end

    def ljust_visible(text, width)
      pad = [width - display_width(text), 0].max
      text + (" " * pad)
    end

    def display_width(text)
      text.each_codepoint.sum do |cp|
        if combining_codepoint?(cp) || zero_width_codepoint?(cp)
          0
        else
          wide_codepoint?(cp) ? 2 : 1
        end
      end
    end

    def combining_codepoint?(cp)
      (cp >= 0x0300 && cp <= 0x036F) ||
        (cp >= 0x1AB0 && cp <= 0x1AFF) ||
        (cp >= 0x1DC0 && cp <= 0x1DFF) ||
        (cp >= 0x20D0 && cp <= 0x20FF) ||
        (cp >= 0xFE20 && cp <= 0xFE2F)
    end

    def zero_width_codepoint?(cp)
      cp == 0x200C || # ZERO WIDTH NON-JOINER
        cp == 0x200D || # ZERO WIDTH JOINER
        (cp >= 0xFE00 && cp <= 0xFE0F) || # variation selectors
        (cp >= 0xE0100 && cp <= 0xE01EF)  # supplemental variation selectors
    end

    def take_by_display_width(text, max_width)
      return ["", text] if max_width <= 0 || text.empty?

      width = 0
      index = 0
      text.each_char do |ch|
        ch_width = display_width(ch)
        break if width + ch_width > max_width

        width += ch_width
        index += ch.length
      end

      [text[0, index], text[index..] || ""]
    end

    def wide_codepoint?(cp)
      (cp >= 0x1100 && cp <= 0x115F) ||
        (cp >= 0x2329 && cp <= 0x232A) ||
        (cp >= 0x2600 && cp <= 0x27BF) ||
        (cp >= 0x2E80 && cp <= 0xA4CF) ||
        (cp >= 0xAC00 && cp <= 0xD7A3) ||
        (cp >= 0xF900 && cp <= 0xFAFF) ||
        (cp >= 0xFE10 && cp <= 0xFE19) ||
        (cp >= 0xFE30 && cp <= 0xFE6F) ||
        (cp >= 0xFF00 && cp <= 0xFF60) ||
        (cp >= 0xFFE0 && cp <= 0xFFE6) ||
        (cp >= 0x1F300 && cp <= 0x1FAFF)
    end

    def format_line(text, width, heart = nil)
      heart_width = 2
      heart = " " if heart.nil?
      heart = clamp(heart, heart_width)
      body_width = [width - heart_width - 1, 1].max
      body = clamp(text.to_s, body_width).ljust(body_width)
      line = "#{body} #{heart}"
      line
    end

    def clamp(text, width)
      return text if text.length <= width

      text[0, width]
    end

    def content_width(width)
      [width - 3, 1].max
    end

    def render_progress_footer(total_posts, selected, width)
      box = build_progress_footer(total_posts, selected, width)
      print box.chomp
    end

    def render_progress_footer_at(total_posts, selected, width, row)
      box = build_progress_footer(total_posts, selected, width)
      box.split("\n").each_with_index do |line, idx|
        print_line_at(row + idx, 0, line, width)
      end
    end

    def build_progress_footer(total_posts, selected, width)
      total = total_posts.to_i
      current = total.zero? ? 0 : selected + 1

      content_width = [width - 4, 1].max
      label = format("%d/%d", current, total)
      label_width = label.length + 1
      bar_inner_width = [content_width - label_width - 2, 1].max
      filled = total.zero? ? 0 : ((current.to_f / total) * bar_inner_width).round
      filled = [[filled, 0].max, bar_inner_width].min
      bar_inner = ("=" * filled) + (" " * (bar_inner_width - filled))
      bar = "[#{bar_inner}]"
      footer = "#{bar}#{label.rjust(label_width)}"
      footer = footer.ljust(content_width)
      footer = theme_text(footer, fg: "primary", bg: "bar_backgrounds")

      box = TTY::Box.frame(width: width, height: 3, padding: [0, 1]) { footer }
      theme_box_borders(box)
    end

    def reply_to_topic(topic_id)
      category_label = category_label_for(topic_id)
      body = compose_body("Reply to topic", category_label: category_label)
      return false if body.nil?

      result = with_errors { @client.create_post(topic_id: topic_id, raw: body) }
      !result.nil?
    end

    def reply_to_post(topic_id, post)
      label = "Reply to post ##{post["post_number"]} (Ctrl+D to finish)"
      context = compose_context(post)
      category_label = category_label_for(topic_id)
      body = compose_body(label, context_lines: context, category_label: category_label)
      return false if body.nil?

      result = with_errors do
        @client.create_post(
          topic_id: topic_id,
          raw: body,
          reply_to_post_number: post["post_number"]
        )
      end
      !result.nil?
    end

    def new_topic_flow
      buffer = +""
      loop do
        clear_screen
        width = TTY::Screen.width
        content = [
          build_header_line("New Topic", @display_url, width - 4),
          "-" * (width - 4),
          "Enter title and press Enter"
        ]
        render_header(content, width)
        print "Title: "
        print buffer
        key = @reader.read_keypress

        case key
        when "\r"
          title = buffer.strip
          return nil if title.empty?
          buffer = title
          break
        when "\u0004", "\u001b"
          return nil
        when "\u007f", "\b"
          buffer.chop! unless buffer.empty?
        else
          buffer << key
        end
      end

      category = pick_category
      category_id = category[:id]
      category_label = category[:label]
      body = compose_body("New Topic: #{buffer}", category_label: category_label)
      return nil if body.nil?

      { title: buffer, raw: body, category: category_id }
    end

    def create_topic_from(data)
      return if data.nil?

      with_errors do
        @client.create_topic(title: data[:title], raw: data[:raw], category: data[:category])
      end
    end

    def pick_category
      info = with_errors { @client.site_info }
      categories = info&.dig("categories") || []
      default_id = info&.dig("default_category_id")

      options = []
      options << { name: "No category", value: nil }
      categories.each do |cat|
        next if cat["read_restricted"]
        options << { name: cat["name"], value: cat["id"] }
      end

      default_index = if default_id
                        options.find_index { |opt| opt[:value] == default_id } || 0
                      else
                        0
                      end

      selected = category_picker(options, default_index)
      name = options[selected][:name] rescue "No category"
      { id: options[selected][:value], label: "Category: #{name}" }
    rescue StandardError
      { id: nil, label: "Category: none" }
    end

    def category_picker(options, selected)
      loop do
        clear_screen
        width = TTY::Screen.width
        content = [
          build_header_line("Select Category", @display_url, width - 4),
          "-" * (width - 4),
          "Use arrows, Enter to select, Esc to cancel"
        ]
        header_height = render_header(content, width)

        max_lines = [TTY::Screen.height - header_height - 1, 1].max
        start_index = [selected - (max_lines / 2), 0].max
        end_index = [start_index + max_lines - 1, options.length - 1].min

        options[start_index..end_index].each_with_index do |opt, idx|
          line_index = start_index + idx
          line = opt[:name]
          line = @pastel.inverse(line) if line_index == selected
          puts line
        end

        key = read_keypress_with_tick
        if key == :__tick__
          @resized = false
          next
        end
        case key
        when "\u001b[A"
          selected = [selected - 1, 0].max
        when "\u001b[B"
          selected = [selected + 1, options.length - 1].min
        when "\r"
          return selected
        when "\u001b"
          return 0
        end
      end
    end

    def category_label_from_topic_data(topic_data)
      category_id = topic_data&.dig("category_id")
      return "Category: none" if category_id.nil?

      name = site_categories[category_id] || "Category #{category_id}"
      "Category: #{name}"
    end

    def normalize_multiline(body)
      return nil if body.nil?
      return body.join("\n") if body.is_a?(Array)

      body
    end

    def compose_body(title, context_lines: [], category_label: nil)
      min_len = 20
      buffer = +""
      cursor = 0

      loop do
        count = buffer.length
        clear_screen
        render_composer_box(title, buffer, cursor, count, min_len, context_lines: context_lines, category_label: category_label)
        key = read_keypress_with_tick
        if key == :__tick__
          @resized = false
          next
        end

        if key.start_with?("\u001b")
          seq = key.length > 1 ? key[1..] : @reader.read_keypress
          case seq
          when "[A"
            cursor = move_cursor_up(buffer, cursor)
          when "[B"
            cursor = move_cursor_down(buffer, cursor)
          when "[C"
            cursor = [cursor + 1, buffer.length].min
          when "[D"
            cursor = [cursor - 1, 0].max
          else
            return nil
          end
          next
        end

        case key
        when "\u0004" # Ctrl+D
          break
        when "\r", "\n"
          buffer.insert(cursor, "\n")
          cursor += 1
        when "\u007f", "\b"
          if cursor.positive?
            buffer.slice!(cursor - 1)
            cursor -= 1
          end
        else
          buffer.insert(cursor, key)
          cursor += key.length
        end
      end

      return nil if buffer.strip.empty?
      return buffer if buffer.strip.length >= min_len

      render_composer_box(title, buffer, cursor, buffer.strip.length, min_len, context_lines: context_lines, category_label: category_label, invalid: true)
      puts "Press any key to try again..."
      @reader.read_keypress
      compose_body(title, context_lines: context_lines, category_label: category_label)
    end

    def render_composer_box(title, buffer, cursor, count, min_len, context_lines: [], category_label: nil, invalid: false)
      width = TTY::Screen.width
      content_width = width - 4
      status = "#{count} / #{min_len}"
      status = if count < min_len
                 @pastel.red(status)
               else
                 @pastel.green(status)
               end
      label = invalid ? "Body too short" : "Compose"

      left = "Chars: #{status} | Arrows: move | Finish: Ctrl+D | New line: Enter | Cancel: Esc"
      right = category_label.to_s
      status_line = build_header_line_visible(left, right, content_width)

      top_line = build_header_line("#{label} #{title}", @display_url, content_width)
      content = [top_line]
      unless context_lines.empty?
        content << "-" * content_width
        content.concat(context_lines)
      end
      content << "-" * content_width
      content << status_line

      box = build_header_box(content, width)
      box_lines = box.split("\n", -1)
      box_lines.pop if box_lines.last == ""
      box_height = box_lines.length

      input_lines = buffer.split("\n", -1)
      input_lines = [""] if input_lines.empty?
      input_start_row = box_height + 1

      screen = Array.new(TTY::Screen.height) { " " * width }
      box_lines.each_with_index do |line, idx|
        screen[idx] = pad_line(line, width)
      end

      input_lines.each_with_index do |line, idx|
        row = input_start_row + idx
        break if row >= screen.length
        screen[row] = pad_line(" #{line}", width)
      end

      clear_screen
      print screen.join("\n")

      line_idx, col = cursor_line_col(buffer, cursor)
      row = input_start_row + line_idx
      col = [col + 1, width - 1].min
      print TTY::Cursor.move_to(col, row)
    end

    def cursor_line_col(buffer, cursor)
      before = buffer[0, cursor] || ""
      lines = before.split("\n", -1)
      line_idx = lines.length - 1
      col = lines.last.to_s.length
      [line_idx, col]
    end

    def move_cursor_up(buffer, cursor)
      lines = buffer.split("\n", -1)
      line_idx, col = cursor_line_col(buffer, cursor)
      return cursor if line_idx.zero?

      new_line_idx = line_idx - 1
      target_line = lines[new_line_idx] || ""
      new_col = [col, target_line.length].min
      line_start = lines[0...new_line_idx].sum { |l| l.length + 1 }
      line_start + new_col
    end

    def move_cursor_down(buffer, cursor)
      lines = buffer.split("\n", -1)
      line_idx, col = cursor_line_col(buffer, cursor)
      return cursor if line_idx >= lines.length - 1

      new_line_idx = line_idx + 1
      target_line = lines[new_line_idx] || ""
      new_col = [col, target_line.length].min
      line_start = lines[0...new_line_idx].sum { |l| l.length + 1 }
      line_start + new_col
    end

    def compose_context(post)
      return [] if post.nil?

      username = post["username"].to_s
      raw = post["raw"].to_s
      lines = parse_markdown_lines(raw, content_width(TTY::Screen.width))
      lines = wrap_and_linkify_lines(lines, content_width(TTY::Screen.width))
      preview = lines.first(3)
      preview = [""] if preview.empty?
      ["Replying to @#{username}:", *preview]
    end

    def category_label_for(topic_id)
      topic = with_errors { @client.topic(topic_id) }
      category_id = topic&.dig("category_id")
      return "Category: none" if category_id.nil?

      categories = site_categories
      name = categories[category_id] || "Category #{category_id}"
      "Category: #{name}"
    end

    def site_categories
      @site_categories ||= begin
        info = with_errors { @client.site_info }
        list = info&.dig("categories") || []
        list.each_with_object({}) { |cat, memo| memo[cat["id"]] = cat["name"].to_s }
      end
    end

    def toggle_like(post)
      if post_liked?(post)
        with_errors { @client.unlike_post(post["id"]) }
      else
        with_errors { @client.like_post(post["id"]) }
      end
    end

    def post_liked?(post)
      summary = post["actions_summary"] || []
      summary.any? { |action| action["id"] == 2 && action["acted"] }
    end

    def fetch_latest
      with_errors { @client.latest_topics }
    end

    def fetch_list(filter, top_period)
      with_errors { @client.list_topics(filter, period: top_period.to_s, username: @api_username) }
    end

    def fetch_more_topics(next_url)
      with_errors { @client.get_url(next_url) }
    end

    def fetch_search(query)
      with_errors { @client.search(query) }
    end

    def fetch_topic(topic_id)
      with_errors { @client.topic(topic_id) }
    end

    def maybe_update_read_state(topic_id, post)
      return if post.nil?

      post_number = post["post_number"].to_i
      return if post_number <= 0

      topic_key = topic_id.to_i
      last_sent = @last_read_post_sent[topic_key].to_i
      return if post_number <= last_sent

      @client.update_topic_read_state(topic_id: topic_key, post_number: post_number)
      @last_read_post_sent[topic_key] = post_number
    rescue StandardError
      nil
    end

    def refresh_pm_unread_count
      data = begin
        @client.get_url("/topics/private-messages-unread.json")
      rescue StandardError
        begin
          @client.list_topics(:private, username: @api_username)
        rescue StandardError
          nil
        end
      end
      @pm_unread_count = unread_private_messages_count(data)
    rescue StandardError
      @pm_unread_count = 0
    end

    def unread_private_messages_count(data)
      topics = data&.dig("topic_list", "topics")
      return 0 unless topics.is_a?(Array)

      topics.count { |topic| private_topic_unread?(topic) }
    end

    def private_topic_unread?(topic)
      return true if topic["unread"] == true || topic["unseen"] == true

      unread_posts = topic["unread_posts"]
      return true if unread_posts.is_a?(Numeric) && unread_posts.positive?

      highest = topic["highest_post_number"]
      last_read = topic["last_read_post_number"]
      return false unless highest.is_a?(Numeric) && last_read.is_a?(Numeric)

      highest > last_read
    end

    def with_errors
      yield
    rescue Faraday::Error => e
      show_error(e)
      nil
    rescue JSON::ParserError => e
      show_error(e)
      nil
    end

    def show_error(error)
      clear_screen
      message = "Error"
      if error.respond_to?(:response) && error.response.is_a?(Hash)
        body = error.response[:body]
        pretty = extract_error_message(body)
        message = "#{message}: #{pretty}" if pretty
      elsif error.respond_to?(:message) && error.message
        message = "#{message}: #{error.message}"
      end
      if message.start_with?("Error:")
        error_body = message.sub("Error:", "").strip
        puts "#{@pastel.bold("Error:")} #{error_body}"
      else
        puts @pastel.bold("Error:")
        puts message
      end
      puts ""
      puts "Press any key to continue..."
      @reader.read_keypress
    end

    def extract_error_message(body)
      return nil if body.nil?

      data = JSON.parse(body.to_s) rescue nil
      return body.to_s unless data.is_a?(Hash)

      if data["errors"].is_a?(Array) && !data["errors"].empty?
        return data["errors"].join("\n")
      end

      if data["error"].is_a?(String)
        return data["error"]
      end

      if data["message"].is_a?(String)
        return data["message"]
      end

      body.to_s
    end

    def search_loop(query)
      return nil if query.nil? || query.strip.empty?

      results = fetch_search(query)
      return nil if results.nil?

      topics_map = {}
      (results["topics"] || []).each { |t| topics_map[t["id"]] = t["title"].to_s }
      posts = results["posts"] || []
      selected = 0

      loop do
        render_search_results(query, posts, topics_map, selected)
        key = read_keypress_with_tick
        if key == :__tick__
          @resized = false
          next
        end
        if @resized
          @resized = false
          next
        end

        case key
        when "\u001b[A"
          selected = [selected - 1, 0].max
        when "\u001b[B"
          selected = [selected + 1, posts.length - 1].min
        when "\r", "\n"
          post = posts[selected]
          return nil unless post
          return { topic_id: post["topic_id"], post_id: post["id"] }
        when "q", "\u001b"
          return nil
        end
      end
    end

    def render_search_results(query, posts, topics_map, selected)
      clear_screen
      width = TTY::Screen.width
      height = TTY::Screen.height
      row_width = [width - 1, 1].max

      top_line = build_header_line(
        "arrows: move | ↵: open | esc: back | q: quit",
        @display_url,
        width - 4
      )
      status = "Search: #{truncate(query, width - 4)}"
      content = [
        top_line,
        "-" * (width - 4),
        status
      ]
      header_height = render_header(content, width)

      if posts.empty?
        puts "No results."
        return
      end

      max_lines = [height - header_height - 1, 1].max
      start_index = [selected - (max_lines / 2), 0].max
      end_index = [start_index + max_lines - 1, posts.length - 1].min
      separator_width = 3
      title_width = [[(row_width / 3.0).floor, 10].max, row_width - separator_width - 10].min
      blurb_width = [row_width - separator_width - title_width, 10].max

      posts[start_index..end_index].each_with_index do |post, idx|
        line_index = start_index + idx
        title = normalize_inline_text(emojify(topics_map[post["topic_id"]] || "Topic #{post["topic_id"]}"))
        blurb = normalize_inline_text(emojify(strip_html(post["blurb"].to_s)))
        title_cell = fit_topic_list_cell(title, title_width, align: :left)
        blurb_cell = fit_topic_list_cell(blurb, blurb_width, align: :left)
        line = "#{title_cell} - #{blurb_cell}"
        line = fit_topic_list_cell(strip_all_ansi(line), row_width, align: :left)
        line = highlight_search_term(line, query)
        line = inverse_preserve_ansi(line) if line_index == selected
        puts line
      end
    end

    def prompt_search_query
      buffer = +""
      loop do
        clear_screen
        width = TTY::Screen.width
        content = [
          build_header_line("Search", @display_url, width - 4),
          "-" * (width - 4),
          "Type query and press Enter"
        ]
        render_header(content, width)
        print "Search: "
        print buffer
        key = @reader.read_keypress

        case key
        when "\r"
          return buffer.strip
        when "\u0004", "\u001b"
          return nil
        when "\u007f", "\b"
          buffer.chop! unless buffer.empty?
        else
          buffer << key
        end
      end
    end

    def strip_html(text)
      text.gsub(/<[^>]*>/, "")
    end

    def highlight_search_term(text, query)
      q = query.to_s.strip
      return text if q.empty?

      pattern = Regexp.new(Regexp.escape(q), Regexp::IGNORECASE)
      text.gsub(pattern) { |m| @pastel.bold(m) }
    rescue RegexpError
      text
    end

    def inverse_preserve_ansi(text)
      # If text contains resets (e.g. bold search matches), restore inverse after each reset.
      "\e[7m#{text.to_s.gsub("\e[0m", "\e[0m\e[7m")}\e[0m"
    end

    def truncate(text, width)
      return text if text.length <= width
      return text[0, width] if width <= 3

      text[0, width - 3] + "..."
    end

    def clear_screen
      print TTY::Cursor.clear_screen
      print TTY::Cursor.move_to(0, 0)
    end

    def read_keypress_with_tick
      loop do
        key = @reader.read_keypress(nonblock: true)
        return key if key
        return :__tick__ if @resized
        sleep(@tick_seconds)
      end
    end

    def trap_resize
      Signal.trap("WINCH") { @resized = true }
    rescue ArgumentError
      nil
    end

    def themed_topic_list_line(number, title, replies, width: nil)
      num_raw = format("%3d", number)
      meta_raw = "(#{replies} replies)"
      safe_title = compact_title_for_width(title, num_raw, meta_raw, width)
      num = theme_text(num_raw, fg: "list_numbers")
      title_text = theme_text(safe_title, fg: "list_text")
      meta = theme_text(meta_raw, fg: "list_meta")
      "#{num}  #{title_text}  #{meta}"
    end

    def themed_pm_topic_list_compact_line(number, title, topic, width: nil)
      num_raw = format("%3d", number)
      meta_raw = "(#{pm_users_label(topic)})"
      safe_title = compact_title_for_width(title, num_raw, meta_raw, width)
      num = theme_text(num_raw, fg: "list_numbers")
      title_text = theme_text(safe_title, fg: "list_text")
      users_text = theme_text(meta_raw, fg: "list_meta")
      "#{num}  #{title_text}  #{users_text}"
    end

    def compact_title_for_width(title, num_raw, meta_raw, width)
      text = normalize_inline_text(title.to_s)
      return text if width.nil? || width <= 0

      # Row format: "<num><2 spaces><title><2 spaces><meta>"
      title_width = width - display_width(num_raw) - display_width(meta_raw) - 4
      title_width = [title_width, 3].max
      truncate_display(text, title_width)
    end

    def topic_list_mode(width)
      return :stats if width >= TOPIC_LIST_WIDE_STATS_MIN
      return :category if width >= TOPIC_LIST_WIDE_CATEGORY_MIN

      :compact
    end

    def themed_topic_list_header(width, mode, filter)
      spec = topic_list_table_spec(width, mode, filter)
      header = build_topic_list_table_row(
        spec[:columns].map do |col|
          { text: col[:label], width: spec[:widths][col[:key]], align: col[:align] }
        end
      )
      theme_text(header, fg: "list_meta")
    end

    def themed_topic_list_row(topic, number, width, mode, filter)
      spec = topic_list_table_spec(width, mode, filter)
      row = build_topic_list_table_row(
        spec[:columns].map do |col|
          {
            text: topic_list_cell_value(topic, number, col[:key], filter),
            width: spec[:widths][col[:key]],
            align: col[:align]
          }
        end
      )
      theme_text(row, fg: "list_text")
    end

    def topic_list_table_spec(width, mode, filter)
      {
        widths: topic_list_widths(width, mode, filter),
        columns: topic_list_columns(mode, filter)
      }
    end

    def topic_list_columns(mode, filter)
      return pm_topic_list_columns if filter == :private

      cols = [
        { key: :idx, label: "#", align: :right },
        { key: :title, label: "Title", align: :left },
        { key: :category, label: "Category", align: :left },
        { key: :replies, label: "Replies", align: :right }
      ]
      return cols unless mode == :stats

      cols + [
        { key: :views, label: "Views", align: :right },
        { key: :users, label: "Users", align: :right }
      ]
    end

    def pm_topic_list_columns
      [
        { key: :idx, label: "#", align: :right },
        { key: :title, label: "Title", align: :left },
        { key: :users, label: "Users", align: :left },
        { key: :replies, label: "Replies", align: :right }
      ]
    end

    def topic_list_cell_value(topic, number, key, filter)
      case key
      when :idx
        number.to_s
      when :title
        topic["title"].to_s
      when :category
        normalize_category_for_table(topic_category_name(topic))
      when :replies
        topic_reply_count(topic).to_s
      when :views
        topic_view_count(topic).to_s
      when :users
        filter == :private ? pm_users_label(topic) : topic_participants_count(topic).to_s
      else
        ""
      end
    end

    def topic_list_widths(width, mode, filter)
      return topic_list_pm_widths(width, mode) if filter == :private
      return topic_list_stats_widths(width) if mode == :stats

      topic_list_category_widths(width)
    end

    def topic_list_pm_widths(width, mode)
      return topic_list_pm_stats_widths(width) if mode == :stats

      topic_list_pm_category_widths(width)
    end

    def build_topic_list_table_row(columns)
      columns.map do |col|
        text = normalize_inline_text(col[:text].to_s)
        fit_topic_list_cell(text, col[:width].to_i, align: col[:align] || :left)
      end.join("  ")
    end

    def normalize_inline_text(text)
      text
        .gsub(/\r\n?/, "\n")
        .tr("\n\t", "  ")
        .gsub(/\s+/, " ")
        .strip
    end

    def fit_topic_list_cell(text, width, align: :left)
      return "" if width <= 0

      clipped = truncate_display(text.to_s, width)
      gap = [width - display_width(clipped), 0].max
      align == :right ? (" " * gap) + clipped : clipped + (" " * gap)
    end

    def truncate_display(text, width)
      return "" if width <= 0
      return clamp_visible(text, width) if width <= 3
      return text if display_width(text) <= width

      head, = take_by_display_width(text, width - 3)
      "#{head}..."
    end

    def topic_list_category_widths(width)
      idx = 3
      replies = 7
      category = [[(width * 0.24).to_i + 5, 21].max, 33].min
      title = [width - idx - category - replies - 6, 20].max
      { idx: idx, title: title, category: category, replies: replies }
    end

    def topic_list_stats_widths(width)
      idx = 3
      replies = 7
      views = 7
      users = 7
      category = [[(width * 0.24).to_i + 5, 21].max, 33].min
      title = [width - idx - category - replies - views - users - 10, 20].max
      { idx: idx, title: title, category: category, replies: replies, views: views, users: users }
    end

    def topic_list_pm_category_widths(width)
      idx = 3
      replies = 7
      users = [[(width * 0.15).to_i, 12].max, 24].min
      title = [width - idx - users - replies - 6, 20].max
      { idx: idx, title: title, users: users, replies: replies }
    end

    def topic_list_pm_stats_widths(width)
      idx = 3
      replies = 7
      users = [[(width * 0.26).to_i, 22].max, 42].min
      title = [width - idx - users - replies - 6, 20].max
      { idx: idx, title: title, users: users, replies: replies }
    end

    def topic_category_name(topic)
      category_id = topic["category_id"]
      return "none" if category_id.nil?

      site_categories[category_id] || "Category #{category_id}"
    end

    def topic_reply_count(topic)
      return topic["reply_count"].to_i if topic.key?("reply_count")

      [topic["posts_count"].to_i - 1, 0].max
    end

    def topic_view_count(topic)
      topic["views"].to_i
    end

    def topic_participants_count(topic)
      return topic["participant_count"].to_i if topic.key?("participant_count")

      posters = topic["posters"]
      return 0 unless posters.is_a?(Array)

      posters.map { |p| p["user_id"] }.compact.uniq.length
    end

    def pm_users_label(topic)
      users = pm_usernames(topic)
      count = users.length
      if count <= 0
        count = topic_participants_count(topic)
        count -= 1 if logged_in_username && count.positive?
      end

      return "#{count} users" if count > 3
      return users.first(3).join(", ") unless users.empty?
      return "#{count} users" if count.positive?

      "-"
    end

    def pm_usernames(topic)
      login = logged_in_username
      participants = topic["participants"]
      if participants.is_a?(Array)
        names = participants.filter_map { |p| p["username"].to_s.strip.downcase }.reject(&:empty?).uniq
        names.reject! { |name| name == login } if login
        return names unless names.empty?
      end

      allowed = topic["allowed_users"]
      if allowed.is_a?(Array)
        names = allowed.filter_map { |u| u["username"].to_s.strip.downcase }.reject(&:empty?).uniq
        names.reject! { |name| name == login } if login
        return names unless names.empty?
      end

      posters = topic["posters"]
      if posters.is_a?(Array)
        names = posters
          .filter_map { |p| p["user_id"] }
          .uniq
          .filter_map { |id| @topic_list_users_by_id[id].to_s.strip.downcase }
          .reject(&:empty?)
          .uniq
        names.reject! { |name| name == login } if login
        return names unless names.empty?
      end

      []
    end

    def merge_topic_list_users(payload)
      users = payload&.dig("users")
      return unless users.is_a?(Array)

      users.each do |user|
        id = user["id"]
        username = user["username"].to_s.strip
        next if id.nil? || username.empty?

        @topic_list_users_by_id[id] = username
      end
    end

    def logged_in_username
      name = @api_username.to_s.strip.downcase
      return nil if name.empty?

      name
    end

    def topic_list_filter_label(filter)
      return "Private Messages" if filter == :private

      filter.to_s.capitalize
    end

    def normalize_category_for_table(text)
      text.to_s
        .gsub(/\p{Extended_Pictographic}/, "")
        .gsub(/\s+/, " ")
        .strip
    end

    def theme_highlight(text)
      fg = theme_color("highlighted_text")
      bg = theme_color("highlighted")
      return @pastel.inverse(text) if fg.nil? && bg.nil?

      theme_text(text, fg: "highlighted_text", bg: "highlighted")
    end

    def theme_header_content(lines)
      return lines if lines.nil? || lines.empty?

      lines.map.with_index do |line, idx|
        if idx == 1
          theme_text(line.to_s, fg: "separators", bg: "bar_backgrounds")
        else
          parts = line.to_s.split("|", -1)
          sep = theme_text("|", fg: "separators", bg: "bar_backgrounds")
          parts.map { |part| theme_text(part, fg: "primary", bg: "bar_backgrounds") }.join(sep)
        end
      end
    end

    def theme_box_borders(box)
      return box unless box

      border_color = theme_color("borders")
      return box if border_color.nil?

      box.gsub(/[┌┐└┘│─]/) { |ch| colorize(ch, fg: border_color) }
    end

    def theme_text(text, fg: nil, bg: nil)
      fg_value = fg.nil? ? nil : theme_color(fg)
      bg_value = bg.nil? ? nil : theme_color(bg)
      return text if fg_value.nil? && bg_value.nil?

      colorize(text, fg: fg_value, bg: bg_value)
    end

    def colorize(text, fg: nil, bg: nil)
      prefix = +""
      prefix << ansi_fg(fg) if fg
      prefix << ansi_bg(bg) if bg
      return text if prefix.empty?

      "#{prefix}#{text}\e[0m"
    end

    def ansi_fg(color)
      return "" if color.nil?
      return "\e[38;5;#{color[:index]}m" if color[:index]

      "\e[38;2;#{color[:r]};#{color[:g]};#{color[:b]}m"
    end

    def ansi_bg(color)
      return "" if color.nil?
      return "\e[48;5;#{color[:index]}m" if color[:index]

      "\e[48;2;#{color[:r]};#{color[:g]};#{color[:b]}m"
    end

    def theme_color(key)
      value = @theme[key.to_s]
      parse_color(value)
    end

    def parse_color(value)
      return nil if value.nil?

      raw = value.to_s.strip.downcase
      return nil if raw.empty? || raw == "none"

      if raw.match?(/\A\d{1,3}\z/)
        idx = raw.to_i
        return nil if idx.negative? || idx > 255

        return { index: idx }
      end

      hex = if raw.match?(/\A#[0-9a-f]{6}\z/)
              raw[1..]
            elsif raw.match?(/\A[0-9a-f]{6}\z/)
              raw
            else
              named_color_hex(raw)
            end
      return nil unless hex

      {
        r: hex[0, 2].to_i(16),
        g: hex[2, 2].to_i(16),
        b: hex[4, 2].to_i(16)
      }
    end

    def named_color_hex(name)
      {
        "black" => "000000",
        "white" => "ffffff",
        "red" => "ff4b4b",
        "green" => "4ecb71",
        "blue" => "4a90e2",
        "yellow" => "ffd166",
        "cyan" => "66d9ef",
        "magenta" => "d38cff",
        "gray" => "9aa0a6",
        "grey" => "9aa0a6"
      }[name]
    end

    def load_theme(theme_name)
      defaults = BUILTIN_THEMES["default"] || {}
      built_in = BUILTIN_THEMES[theme_name] || {}
      file_themes = load_theme_file
      from_file = file_themes[theme_name] || {}
      defaults.merge(built_in).merge(from_file)
    rescue StandardError
      BUILTIN_THEMES["default"] || {}
    end

    def load_theme_file
      path = ENV["TERMCOURSE_THEME_FILE"]
      path = default_theme_path if path.nil? || path.strip.empty?
      return {} unless File.file?(path)

      parsed = YAML.safe_load(File.read(path)) || {}
      themes = parsed["themes"] if parsed.is_a?(Hash)
      themes = parsed unless themes.is_a?(Hash)
      return {} unless themes.is_a?(Hash)

      themes.each_with_object({}) do |(name, properties), memo|
        next unless properties.is_a?(Hash)

        memo[name.to_s.downcase] = properties.transform_keys(&:to_s)
      end
    rescue StandardError
      {}
    end

    def default_theme_path
      local = File.expand_path("theme.yml", Dir.pwd)
      return local if File.file?(local)

      File.expand_path("~/.config/termcourse/theme.yml")
    end
  end
end
