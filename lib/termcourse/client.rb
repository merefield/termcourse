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

    def login(username:, password:, otp: nil, otp_method: 1)
      ensure_csrf
      payload = { login: username, password: password }
      if otp
        payload[:second_factor_token] = otp
        payload[:second_factor_method] = otp_method
      end
      post_json("/session.json", payload)
    end

    def current_user
      get_json("/session/current.json")
    end

    def ensure_csrf
      return @csrf_token if @csrf_token

      token = fetch_csrf_token
      @csrf_token = token if token
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
      nil
    end
  end
end
