# frozen_string_literal: true

require "cgi"
require "json"
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
      render_header(content, width)

      if topics.empty?
        puts "No topics found."
        return
      end

      header_height = content.length + 2
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
      clear_screen
      width = TTY::Screen.width
      height = TTY::Screen.height
      title = topic_data["title"].to_s

      top_line = build_header_line(
        "arrows: move | l: like | r: reply topic | p: reply post | esc: back | q: quit",
        @display_url,
        width - 4
      )
      topic_line = "Topic: #{truncate(title, width - 4)}"
      content = [
        top_line,
        "-" * (width - 4),
        topic_line
      ]
      render_header(content, width)

      list_height_lines = height - 8
      printed = render_post_list(posts, selected, scroll_offsets[selected], list_height_lines, width)
      filler = list_height_lines - printed
      filler.times { puts "" } if filler.positive?
      render_progress_footer(posts.length, selected, width)
    end

    def render_post_list(posts, selected, scroll_offset, list_height_lines, width)
      return if posts.empty?

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

      printed = 0
      rendered.each_with_index do |item, idx|
        item[:lines].each do |line|
          puts line
          printed += 1
        end
        next if idx == rendered.length - 1

        puts "-" * width
        printed += 1
      end

      printed
    end

    def build_post_block(post, expanded, width)
      liked = post_liked?(post)
      liked_marker = ""
      username = post["username"].to_s
      heart = liked ? "♥" : "♡"
      header = "#{liked_marker}@#{username}"

      body_width = content_width(width)
      lines = parse_markdown_lines(post["raw"].to_s, body_width)
      content_lines = wrap_and_linkify_lines(lines, body_width)

      if expanded
        ([format_line(header, width, heart)] + content_lines).map { |line| highlight(line) }
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
              if current_len + token.length <= width
                append.call(token, token.length)
              else
                flush.call
              end
              next
            end

            if token.length > width
              flush.call if current_len.positive?
              token_chars = token
              while token_chars.length.positive?
                take = [width, token_chars.length].min
                append.call(token_chars[0, take], take)
                token_chars = token_chars[take..]
                flush.call if current_len == width
              end
              next
            end

            if current_len + token.length > width
              flush.call
            end
            append.call(token, token.length)
          end
        else
          url = segment[:text]
          display = CGI.unescape(url)
          flush.call if current_len.positive? && display.length > (width - current_len)

          if display.length <= width
            append.call(osc8(url, display), display.length)
          else
            while display.length.positive?
              take = [width, display.length].min
              piece = display[0, take]
              display = display[take..]
              append.call(osc8(url, piece), piece.length)
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
      "\e]8;;#{url}\a#{text}\e]8;;\a"
    end

    def emojify(text)
      text
        .gsub(":heart:", "♥")
        .gsub(":pizza:", "🍕")
        .gsub(":smile:", "😄")
        .gsub(":thumbsup:", "👍")
        .gsub(":fire:", "🔥")
        .gsub(":star:", "★")
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
      content = lines.join("\n")
      height = lines.length + 2
      header = TTY::Box.frame(width: width, height: height, padding: [0, 1]) { content }
      print header.chomp
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

      box = TTY::Box.frame(width: width, height: 3, padding: [0, 1]) { footer }
      print box.chomp
    end

    def reply_to_topic(topic_id)
      body = compose_body("Reply to topic")
      return false if body.nil?

      with_errors { @client.create_post(topic_id: topic_id, raw: body) }
      true
    end

    def reply_to_post(topic_id, post)
      label = "Reply to post ##{post["post_number"]} (Ctrl+D to finish)"
      body = compose_body(label)
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

    def compose_body(title)
      min_len = 20
      body = nil

      loop do
        clear_screen
        render_composer_box(title, body.to_s.length, min_len)
        input = @prompt.multiline("#{title} (Ctrl+D to finish)")
        body = normalize_multiline(input)
        return nil if body.nil? || body.strip.empty?

        return body if body.strip.length >= min_len

        render_composer_box(title, body.strip.length, min_len, invalid: true)
        puts "Press any key to try again..."
        @reader.read_keypress
      end
    end

    def render_composer_box(title, count, min_len, invalid: false)
      width = TTY::Screen.width
      status = "#{count} / #{min_len}"
      status = if count < min_len
                 @pastel.red(status)
               else
                 @pastel.green(status)
               end
      label = invalid ? "Body too short" : "Compose"
      content = [
        "#{label}: #{title}",
        "-" * (width - 4),
        "Chars: #{status}"
      ]
      render_header(content, width)
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
