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
        debug_enabled = ENV.fetch("TERMCOURSE_LOGIN_DEBUG", "0") == "1"
        client.set_debug(debug_enabled)
        if debug_enabled
          File.open("/tmp/termcourse_login_debug.txt", "a") do |f|
            f.puts("[#{Time.now.utc.iso8601}] prompt_complete username=#{username}")
          end
        end
        login = client.login(username: username, password: password)
        if mfa_required?(login)
          method = mfa_method_from(login, prompt)
          otp_label = method == 2 ? "Enter backup code:" : "Enter 2FA code:"
          otp = prompt.ask(otp_label)
          login = client.login(username: username, password: password, otp: otp, otp_method: method)
        end
        current = client.current_user
        login_user = if current.is_a?(Hash) && current["current_user"].is_a?(Hash)
                       current["current_user"]["username"]
                     elsif login.is_a?(Hash)
                       (login.dig("user", "username") || login.dig("current_user", "username") || login["username"])
                     end
        if login_user
          ui = UI.new(base_url, client: client, api_username: login_user)
        else
          warn "Login failed."
          return 1
        end
      end

      ui.run
      0
    end

    def mfa_required?(login)
      return false unless login.is_a?(Hash)

      return true if login["second_factor_required"] || login["requires_second_factor"]
      return true if login["second_factor"] || login["second_factor_methods"]
      return true if login["reason"] == "invalid_second_factor_method"
      error = login["error"].to_s.downcase
      return true if error.include?("second factor") || error.include?("two factor")

      false
    end

    def mfa_method_from(login, prompt)
      methods = login["second_factor_methods"]
      return methods.first if methods.is_a?(Array) && methods.first

      options = []
      options << { label: "TOTP (Recommended)", value: 1 } if login["totp_enabled"]
      options << { label: "Backup code", value: 2 } if login["backup_enabled"]
      return options.first[:value] if options.length == 1

      if options.length > 1
        choice = prompt.select("Choose 2FA method:", options.map { |o| o[:label] })
        selected = options.find { |o| o[:label] == choice }
        return selected[:value] if selected
      end

      1
    end
  end
end
