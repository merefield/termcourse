# frozen_string_literal: true

require "optparse"
require "dotenv/load"

module Termcourse
  class CLI
    def initialize(argv)
      @argv = argv
    end

    def run
      options = {
        api_key: ENV["DISCOURSE_API_KEY"],
        api_username: ENV["DISCOURSE_API_USERNAME"],
        username: ENV["DISCOURSE_USERNAME"],
        password: ENV["DISCOURSE_PASSWORD"],
        force_login: false
      }

      parser = OptionParser.new do |opts|
        opts.banner = "Usage: termcourse [options] <discourse_url>"

        opts.on("--api-key KEY", "Discourse API key (or DISCOURSE_API_KEY)") do |value|
          options[:api_key] = value
        end

        opts.on("--api-username USER", "Discourse API username (or DISCOURSE_API_USERNAME)") do |value|
          options[:api_username] = value
        end

        opts.on("--username USER", "Discourse username/email (or DISCOURSE_USERNAME)") do |value|
          options[:username] = value
        end

        opts.on("--password PASS", "Discourse password (or DISCOURSE_PASSWORD)") do |value|
          options[:password] = value
        end

        opts.on("--login", "Force username/password login (ignore API key)") do
          options[:force_login] = true
        end

        opts.on("-h", "--help", "Show help") do
          puts opts
          return 0
        end
      end

      parser.parse!(@argv)
      base_url = @argv.first

      if base_url.nil? || base_url.strip.empty?
        warn "Missing discourse_url."
        warn parser
        return 1
      end

      ui = nil
      if !options[:force_login] &&
         options[:api_key] && !options[:api_key].strip.empty? &&
         options[:api_username] && !options[:api_username].strip.empty?
        begin
          client = Client.new(base_url, api_key: options[:api_key], api_username: options[:api_username])
          current = client.current_user
          if current.is_a?(Hash) && current["current_user"].is_a?(Hash)
            ui = UI.new(base_url, client: client, api_username: options[:api_username])
          end
        rescue Faraday::Error
          ui = nil
        end
      end

      unless ui
        prompt = TTY::Prompt.new
        username = options[:username]
        password = options[:password]
        username = prompt.ask("Username or email:") if username.nil? || username.strip.empty?
        password = prompt.mask("Password:") if password.nil? || password.strip.empty?
        if username.nil? || username.strip.empty? || password.nil? || password.strip.empty?
          warn "Missing auth. Provide API key or username/password."
          warn "API key: DISCOURSE_API_KEY + DISCOURSE_API_USERNAME"
          warn "Login: DISCOURSE_USERNAME + DISCOURSE_PASSWORD"
          return 1
        end

        client = Client.new(base_url)
        login = client.login(username: username, password: password)
        if login.is_a?(Hash) && (login["second_factor_required"] || login["requires_second_factor"])
          otp = prompt.ask("Enter 2FA code:")
          login = client.login(username: username, password: password, otp: otp)
        end
        current = client.current_user
        if current.is_a?(Hash) && current["current_user"].is_a?(Hash)
          ui = UI.new(base_url, client: client, api_username: current["current_user"]["username"])
        else
          warn "Login failed."
          return 1
        end
      end

      ui.run
      0
    end
  end
end
