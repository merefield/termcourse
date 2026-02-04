# frozen_string_literal: true

require "cgi"
require "json"
require "time"
require "tty-screen"
require "tty-cursor"
require "tty-box"
require "tty-reader"
require "tty-prompt"
require "tty-markdown"
require "pastel"

module Termcourse
  class UI
    def initialize(base_url, api_key:, api_username:)
      @client = Client.new(base_url, api_key: api_key, api_username: api_username)
      @reader = TTY::Reader.new
      @prompt = TTY::Prompt.new
      @pastel = Pastel.new
      @api_username = api_username
      @base_url = base_url
      @display_url = base_url.sub(%r{\Ahttps?://}i, "")
      @links_enabled = ENV.fetch("TERMCOURSE_LINKS", "1") != "0"
      @emoji_enabled = ENV.fetch("TERMCOURSE_EMOJI", "1") != "0"
      @debug_enabled = ENV.fetch("TERMCOURSE_DEBUG", "0") == "1"
      @resized = false
      trap_resize
    end

    def run
      filter = :latest
      top_period = :monthly
      loop do
        topics_data = fetch_list(filter, top_period)
        return if topics_data.nil?

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
        next if result == :reload

        topic_result = topic_loop(result)
        break if topic_result == :quit
      end
    end

    private

    def topic_list_loop(topics, next_url, filter, top_period)
      selected = 0
      loading = false
      filters = %i[latest hot new unread top]
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
        key = @reader.read_keypress
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
            next_url = more&.dig("topic_list", "more_topics_url")
            topics.concat(more_topics)
            loading = false
          end
        when "\r" # enter
          topic = topics[selected]
          return topic["id"] if topic
        when "f"
          filter_index = (filter_index + 1) % filters.length
          return { filter: filters[filter_index] }
        when "p"
          period_index = (period_index + 1) % top_periods.length
          return { top_period: top_periods[period_index] }
        when "g"
          return :reload
        when "q", "\u001b"
          return :quit
        end
      end
    end

    def topic_loop(topic_id)
      topic_data = fetch_topic(topic_id)
      return if topic_data.nil?

      posts = topic_data.dig("post_stream", "posts") || []
      selected = 0
      scroll_offsets = Hash.new(0)

      loop do
        render_topic(topic_data, posts, selected, scroll_offsets)
        key = @reader.read_keypress
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
        when "q"
          return :quit
        when "\u001b", "\u007f"
          return
        end
      end
    end

    def render_topic_list(topics, selected, filter:, top_period:, loading: false)
      clear_screen
      width = TTY::Screen.width
      height = TTY::Screen.height

      top_line = build_header_line(
        "arrows: move | enter: open | f: filter | p: period | g: refresh | q: quit",
        @display_url,
        width - 4
      )
      status_label = "Topic List: #{filter.to_s.capitalize}"
      status_label += " (#{top_period.to_s.capitalize})" if filter == :top
      status = loading ? "#{status_label} | Loading more..." : status_label
      content = [
        top_line,
        "-" * (width - 4),
        status
      ]
      header_height = render_header(content, width)

      if topics.empty?
        puts "No topics found."
        return
      end

      max_lines = [height - header_height - 1, 1].max
      start_index = [selected - (max_lines / 2), 0].max
      end_index = [start_index + max_lines - 1, topics.length - 1].min

      topics[start_index..end_index].each_with_index do |topic, idx|
        line_index = start_index + idx
        replies = topic["posts_count"].to_i - 1
        title = truncate(topic["title"].to_s, width - 10)
        line = format("%3d  %s  (%d replies)", line_index + 1, title, replies)
        line = @pastel.inverse(line) if line_index == selected
        puts line
      end
    end

    def render_topic(topic_data, posts, selected, scroll_offsets)
      width = TTY::Screen.width
      height = TTY::Screen.height
      title = topic_data["title"].to_s

      top_line = build_header_line(
        "arrows: move | l: like | r: reply topic | p: reply post | esc: back | q: quit",
        @display_url,
        width - 4
      )
      topic_line = "Topic: #{truncate(title, width - 4)}"
      header_lines = [
        top_line,
        "-" * (width - 4),
        topic_line
      ]
      header_box = build_header_box(header_lines, width)
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
        lines << "-" * width if idx != rendered.length - 1
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
      lines = parse_markdown_lines(post["raw"].to_s, body_width)
      content_lines = wrap_and_linkify_lines(lines, body_width)

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
      content = TTY::Markdown.parse(raw.to_s, width: width)
      content = content.gsub("\r", "")
      content = content.gsub("\t", "  ")
      content = content.tr("\u2028\u2029\u0085", "   ")
      content = strip_invisible(content)
      content = strip_control_chars(content)
      content = strip_ansi(content)
      lines = content.split("\n").map { |line| emojify(line) }
      lines = [""] if lines.empty?
      lines
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

    def render_header(lines, width)
      header = build_header_box(lines, width)
      print header.chomp
      print "\n"
      header.split("\n").length + 1
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

    def display_width(text)
      text.each_codepoint.sum do |cp|
        if combining_codepoint?(cp)
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

      TTY::Box.frame(width: width, height: 3, padding: [0, 1]) { footer }
    end

    def reply_to_topic(topic_id)
      body = compose_body("Reply to topic")
      return false if body.nil?

      with_errors { @client.create_post(topic_id: topic_id, raw: body) }
      true
    end

    def reply_to_post(topic_id, post)
      label = "Reply to post ##{post["post_number"]} (Ctrl+D to finish)"
      context = compose_context(post)
      body = compose_body(label, context_lines: context)
      return false if body.nil?

      with_errors do
        @client.create_post(
          topic_id: topic_id,
          raw: body,
          reply_to_post_number: post["post_number"]
        )
      end
      true
    end

    def normalize_multiline(body)
      return nil if body.nil?
      return body.join("\n") if body.is_a?(Array)

      body
    end

    def compose_body(title, context_lines: [])
      min_len = 20
      body = nil

      loop do
        body = read_multiline_input(title, min_len, context_lines: context_lines)
        return nil if body.nil? || body.strip.empty?

        return body if body.strip.length >= min_len

        render_composer_box(title, body.strip.length, min_len, context_lines: context_lines, invalid: true)
        puts "Press any key to try again..."
        @reader.read_keypress
      end
    end

    def read_multiline_input(title, min_len, context_lines: [])
      buffer = +""
      loop do
        count = buffer.length
        clear_screen
        render_composer_box(title, count, min_len, context_lines: context_lines)
        print "> "
        print buffer.split("\n").last.to_s
        key = @reader.read_keypress

        case key
        when "\u0004" # Ctrl+D
          break
        when "\r"
          buffer << "\n"
        when "\u007f", "\b"
          buffer.chop! unless buffer.empty?
        when "\u001b"
          next
        else
          buffer << key
        end
      end

      buffer
    end

    def render_composer_box(title, count, min_len, context_lines: [], invalid: false)
      width = TTY::Screen.width
      status = "#{count} / #{min_len}"
      status = if count < min_len
                 @pastel.red(status)
               else
                 @pastel.green(status)
               end
      label = invalid ? "Body too short" : "Compose"
      content = ["#{label}: #{title}"]
      unless context_lines.empty?
        content << "-" * (width - 4)
        content.concat(context_lines)
      end
      content << "-" * (width - 4)
      content << "Chars: #{status}"
      render_header(content, width)
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
      with_errors { @client.list_topics(filter, period: top_period.to_s) }
    end

    def fetch_more_topics(next_url)
      with_errors { @client.get_url(next_url) }
    end

    def fetch_topic(topic_id)
      with_errors { @client.topic(topic_id) }
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
      message = "Error: #{error.class} - #{error.message}"
      if error.respond_to?(:response) && error.response.is_a?(Hash)
        body = error.response[:body]
        message = "#{message}\n#{body}" if body
      end
      puts message
      puts "Press any key to continue..."
      @reader.read_keypress
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

    def trap_resize
      Signal.trap("WINCH") { @resized = true }
    rescue ArgumentError
      nil
    end
  end
end
