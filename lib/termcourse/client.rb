# frozen_string_literal: true

require "faraday"
require "faraday/retry"
require "json"

module Termcourse
  class Client
    def initialize(base_url, api_key:, api_username:)
      @base_url = base_url.sub(%r{/+$}, "")
      @api_key = api_key
      @api_username = api_username

      @connection = Faraday.new(@base_url) do |f|
        f.request :json
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
      {
        "Api-Key" => @api_key,
        "Api-Username" => @api_username,
        "Content-Type" => "application/json",
        "Accept" => "application/json"
      }
    end
  end
end
