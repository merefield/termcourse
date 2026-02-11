# frozen_string_literal: true

require "faraday"
require "faraday/retry"
require "faraday-cookie_jar"
require "http/cookie_jar"
require "json"
require "socket"
require "uri"

module Termcourse
  class Client
    def initialize(base_url, api_key: nil, api_username: nil)
      @base_url = base_url.sub(%r{/+$}, "")
      @api_key = api_key
      @api_username = api_username
      @csrf_token = nil
      @cookie_jar = HTTP::CookieJar.new
      @ipv4_address = resolve_ipv4_address
      @prefer_ipv4 = false

      @connection = build_connection
      @ipv4_connection = build_connection(ipv4: true)
    end

    def latest_topics
      get_json("/latest.json")
    end

    def list_topics(filter, period: "weekly", username: nil)
      path = case filter
             when :latest then "/latest.json"
             when :hot then "/hot.json"
             when :private
               if username && !username.to_s.strip.empty?
                 "/topics/private-messages/#{username}.json"
               else
                 "/topics/private-messages.json"
               end
             when :new then "/new.json"
             when :unread then "/unread.json"
             when :top then "/top.json"
             else "/latest.json"
             end

      params = {}
      params[:period] = period if filter == :top
      get_json(path, params)
    rescue Faraday::ResourceNotFound
      if filter == :private && path.include?("/topics/private-messages/")
        return get_json("/topics/private-messages.json", params)
      end
      raise
    end

    def search(query)
      get_json("/search.json", q: query)
    end

    def get_bytes(path_or_url, max_bytes: nil, redirect_limit: 4)
      response = perform_request(:get, path_or_url, nil)
      if response.status >= 300 && response.status < 400
        raise "too many redirects" if redirect_limit <= 0

        location = response.headers["location"] || response.headers["Location"]
        raise "redirect without location" if location.nil? || location.to_s.strip.empty?

        next_url = if location.start_with?("http://", "https://")
                     location
                   else
                     URI.join("#{@base_url}/", location).to_s
                   end
        return get_bytes(next_url, max_bytes: max_bytes, redirect_limit: redirect_limit - 1)
      end

      body = response.body.to_s
      if max_bytes && body.bytesize > max_bytes
        raise "image too large"
      end
      raise "empty image body" if body.empty?
      body
    end

    def get_url(path_or_url)
      if path_or_url.start_with?("http://", "https://")
        response = @connection.get(path_or_url, nil, headers)
      else
        response = @connection.get(path_or_url, nil, headers)
      end
      parse_json(response.body)
    end

    def topic(id)
      get_json("/t/#{id}.json", print: "true", include_raw: "true")
    end

    def like_post(post_id)
      post_json("/post_actions.json", id: post_id, post_action_type_id: 2)
    end

    def unlike_post(post_id)
      delete_json("/post_actions/#{post_id}.json", post_action_type_id: 2)
    end

    def create_post(topic_id:, raw:, reply_to_post_number: nil)
      payload = { topic_id: topic_id, raw: raw }
      payload[:reply_to_post_number] = reply_to_post_number if reply_to_post_number
      post_json("/posts.json", payload)
    end

    def create_topic(title:, raw:, category: nil)
      payload = { title: title, raw: raw }
      payload[:category] = category if category
      post_json("/posts.json", payload)
    end

    def site_info
      get_json("/site.json")
    end

    def update_topic_read_state(topic_id:, post_number:, topic_time_ms: 1200)
      pn = post_number.to_i
      return nil if pn <= 0

      topic_id = topic_id.to_i
      timing_key = pn.to_s
      timing_val = topic_time_ms.to_i
      payloads = [
        ["/topics/timings", { topic_id: topic_id, topic_time: timing_val, timings: { timing_key => timing_val } }],
        ["/topics/timings", { topic_id: topic_id, topic_time: timing_val, timings: { timing_key => timing_val.to_s } }],
        ["/t/#{topic_id}/timings", { topic_time: timing_val, timings: { timing_key => timing_val } }],
        ["/t/#{topic_id}/timings", { topic_time: timing_val, timings: { timing_key => timing_val.to_s } }]
      ]

      payloads.each do |path, payload|
        begin
          response = perform_request(:post, path, payload)
          return parse_json(response.body)
        rescue Faraday::ClientError
          next
        end
      end
      nil
    rescue Faraday::Error
      nil
    end

    def login(username:, password:, otp: nil, otp_method: 1)
      debug_log("login_start")
      ensure_csrf
      payload = { login: username, password: password }
      if otp
        payload[:second_factor_token] = otp
        payload[:second_factor_method] = otp_method
      end
      debug_log("login_request username=#{username} otp=#{otp ? "yes" : "no"} method=#{otp_method}")
      response = post_json("/session.json", payload)
      debug_log("login_response #{response.inspect}")
      response
    rescue Faraday::ClientError => e
      parsed = parse_error_body(e)
      debug_log("login_error #{parsed.inspect}")
      parsed
    end

    def set_debug(enabled)
      @debug_enabled = enabled
    end

    def current_user
      get_json("/session/current.json")
    rescue Faraday::ResourceNotFound
      nil
    end

    def ensure_csrf
      return @csrf_token if @csrf_token

      token = fetch_csrf_token
      @csrf_token = token if token
      debug_log("csrf_token #{token ? "ok" : "missing"}")
      @csrf_token
    end

    private

    def get_json(path, params = {})
      response = perform_request(:get, path, params)
      parse_json(response.body)
    end

    def post_json(path, payload)
      response = perform_request(:post, path, payload)
      parse_json(response.body)
    end

    def delete_json(path, params = nil)
      response = perform_request(:delete, path, params)
      parse_json(response.body)
    end

    def parse_json(body)
      return {} if body.nil? || body.strip.empty?

      JSON.parse(body)
    end

    def headers
      headers = {
        "Content-Type" => "application/json",
        "Accept" => "application/json",
        "User-Agent" => "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"
      }
      headers["Api-Key"] = @api_key if @api_key && !@api_key.strip.empty?
      headers["Api-Username"] = @api_username if @api_username && !@api_username.strip.empty?
      headers["X-CSRF-Token"] = @csrf_token if @csrf_token && !@csrf_token.strip.empty?
      headers
    end

    def fetch_csrf_token
      data = get_json("/session/csrf.json")
      return data["csrf"] if data.is_a?(Hash) && data["csrf"]

      html = perform_request(:get, "/").body.to_s
      match = html.match(/name=\"csrf-token\" content=\"([^\"]+)\"/)
      match ? match[1] : nil
    rescue Faraday::Error
      debug_log("csrf_fetch_error")
      nil
    end

    def perform_request(method, path_or_url, payload = nil, use_ipv4: false, allow_ipv4_retry: true)
      use_ipv4 = true if @prefer_ipv4
      method_name = method.to_s.upcase
      debug_log("http_request method=#{method_name} path=#{path_or_url} ipv4=#{use_ipv4 ? "yes" : "no"}")
      start = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      connection = use_ipv4 ? @ipv4_connection : @connection
      response = case method
                 when :get
                   connection.get(path_or_url, payload, headers)
                 when :post
                   connection.post(path_or_url, payload, headers)
                 when :delete
                   connection.delete(path_or_url, payload, headers)
                 else
                   raise ArgumentError, "Unsupported method: #{method}"
                 end
      elapsed_ms = ((Process.clock_gettime(Process::CLOCK_MONOTONIC) - start) * 1000).round(1)
      debug_log("http_response method=#{method_name} path=#{path_or_url} status=#{response.status} ms=#{elapsed_ms} ipv4=#{use_ipv4 ? "yes" : "no"}")
      response
    rescue Faraday::Error => e
      elapsed_ms = ((Process.clock_gettime(Process::CLOCK_MONOTONIC) - start) * 1000).round(1)
      debug_log("http_error method=#{method_name} path=#{path_or_url} error=#{e.class} ms=#{elapsed_ms} ipv4=#{use_ipv4 ? "yes" : "no"}")
      if allow_ipv4_retry && !use_ipv4 && ipv4_retryable?(e)
        debug_log("http_retry_ipv4 method=#{method_name} path=#{path_or_url}")
        @prefer_ipv4 = true
        return perform_request(method, path_or_url, payload, use_ipv4: true, allow_ipv4_retry: false)
      end
      raise
    end

    def ipv4_retryable?(error)
      return true if error.is_a?(Faraday::TimeoutError)
      return true if error.is_a?(Faraday::ConnectionFailed) &&
        (error.cause.is_a?(Net::OpenTimeout) || error.message.to_s.include?("execution expired"))

      false
    end

    def build_connection(ipv4: false)
      Faraday.new(@base_url) do |f|
        f.request :json
        f.use Faraday::CookieJar, jar: @cookie_jar
        f.request :retry, max: 2, interval: 0.1, backoff_factor: 2
        f.response :raise_error
        f.options.open_timeout = 3
        f.options.timeout = 15
        if ipv4
          f.adapter :net_http do |http|
            http.ipaddr = @ipv4_address if @ipv4_address
          end
        else
          f.adapter Faraday.default_adapter
        end
      end
    end

    def resolve_ipv4_address
      host = URI(@base_url).host
      return nil if host.nil? || host.strip.empty?

      Addrinfo.getaddrinfo(host, nil, Socket::AF_INET, Socket::SOCK_STREAM).first&.ip_address
    rescue SocketError, ArgumentError
      nil
    end

    def parse_error_body(error)
      return { "__http_status" => nil } unless error.respond_to?(:response)

      status = error.response[:status]
      body = error.response[:body]
      parsed = parse_json(body.to_s)
      parsed = { "error" => body.to_s } unless parsed.is_a?(Hash)
      parsed["__http_status"] = status
      parsed
    rescue JSON::ParserError
      { "error" => error.message, "__http_status" => error.response[:status] }
    end

    def debug_log(message)
      return unless @debug_enabled

      File.open("/tmp/termcourse_http_debug.txt", "a") do |f|
        f.puts("[#{Time.now.utc.iso8601}] #{message}")
      end
    rescue StandardError
      nil
    end
  end
end
