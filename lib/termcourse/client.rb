# frozen_string_literal: true

require "faraday"
require "faraday/retry"
require "faraday-cookie_jar"
require "http/cookie_jar"
require "json"

module Termcourse
  class Client
    def initialize(base_url, api_key: nil, api_username: nil)
      @base_url = base_url.sub(%r{/+$}, "")
      @api_key = api_key
      @api_username = api_username
      @csrf_token = nil
      @cookie_jar = HTTP::CookieJar.new

      @connection = Faraday.new(@base_url) do |f|
        f.request :json
        f.use Faraday::CookieJar, jar: @cookie_jar
        f.request :retry, max: 2, interval: 0.1, backoff_factor: 2
        f.response :raise_error
        f.adapter Faraday.default_adapter
      end
    end

    def latest_topics
      get_json("/latest.json")
    end

    def list_topics(filter, period: "weekly")
      path = case filter
             when :latest then "/latest.json"
             when :hot then "/hot.json"
             when :new then "/new.json"
             when :unread then "/unread.json"
             when :top then "/top.json"
             else "/latest.json"
             end

      params = {}
      params[:period] = period if filter == :top
      get_json(path, params)
    end

    def search(query)
      get_json("/search.json", q: query)
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
      response = @connection.get(path, params, headers)
      parse_json(response.body)
    end

    def post_json(path, payload)
      response = @connection.post(path, payload, headers)
      parse_json(response.body)
    end

    def delete_json(path, params = nil)
      response = @connection.delete(path, params, headers)
      parse_json(response.body)
    end

    def parse_json(body)
      return {} if body.nil? || body.strip.empty?

      JSON.parse(body)
    end

    def headers
      headers = {
        "Content-Type" => "application/json",
        "Accept" => "application/json"
      }
      headers["Api-Key"] = @api_key if @api_key && !@api_key.strip.empty?
      headers["Api-Username"] = @api_username if @api_username && !@api_username.strip.empty?
      headers["X-CSRF-Token"] = @csrf_token if @csrf_token && !@csrf_token.strip.empty?
      headers
    end

    def fetch_csrf_token
      data = get_json("/session/csrf.json")
      return data["csrf"] if data.is_a?(Hash) && data["csrf"]

      html = @connection.get("/").body.to_s
      match = html.match(/name=\"csrf-token\" content=\"([^\"]+)\"/)
      match ? match[1] : nil
    rescue Faraday::Error
      debug_log("csrf_fetch_error")
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

      File.open("/tmp/termcourse_login_debug.txt", "a") do |f|
        f.puts("[#{Time.now.utc.iso8601}] #{message}")
      end
    rescue StandardError
      nil
    end
  end
end
