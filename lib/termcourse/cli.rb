# frozen_string_literal: true

require "optparse"
require "dotenv/load"
require "uri"
require "yaml"

module Termcourse
  class CLI
    def initialize(argv)
      @argv = argv
    end

    def run
      @locale = Localization.resolve_locale(extract_lang_option(@argv))
      options = {
        api_key: nil,
        api_username: nil,
        username: nil,
        password: nil,
        theme: nil,
        lang: nil
      }

      parser = OptionParser.new do |opts|
        opts.banner = t("cli.usage")

        opts.on("--api-key KEY", t("cli.help.api_key")) do |value|
          options[:api_key] = value
        end

        opts.on("--api-username USER", t("cli.help.api_username")) do |value|
          options[:api_username] = value
        end

        opts.on("--username USER", t("cli.help.username")) do |value|
          options[:username] = value
        end

        opts.on("--password PASS", t("cli.help.password")) do |value|
          options[:password] = value
        end

        opts.on("--theme NAME", t("cli.help.theme")) do |value|
          options[:theme] = value
        end

        opts.on("--lang LANG", t("cli.help.lang")) do |value|
          options[:lang] = value
          @locale = Localization.resolve_locale(value)
        end

        opts.on("-h", "--help", t("cli.help.show")) do
          puts opts
          puts
          puts t("cli.help.core_env")
          help_env_variables.each do |name, desc|
            puts "  #{name.ljust(28)} #{desc}"
          end
          return 0
        end
      end

      parser.parse!(@argv)
      base_url = normalize_base_url(@argv.first)

      if base_url.nil? || base_url.strip.empty?
        warn t("cli.errors.missing_url")
        warn parser
        return 1
      end

      site_creds = load_site_credentials(base_url)
      preferred_auth = site_creds[:auth]
      debug_enabled = ENV.fetch("TERMCOURSE_HTTP_DEBUG", "0") == "1"
      theme_name = options[:theme]
      locale = Localization.resolve_locale(options[:lang])
      @locale = locale

      username = options[:username]
      api_key = options[:api_key]
      api_username = options[:api_username]
      password = options[:password]
      username ||= site_creds[:username]
      password ||= site_creds[:password]
      api_key ||= site_creds[:api_key]
      api_username ||= site_creds[:api_username]
      username ||= ENV["DISCOURSE_USERNAME"]
      password ||= ENV["DISCOURSE_PASSWORD"]
      api_key ||= ENV["DISCOURSE_API_KEY"]
      api_username ||= ENV["DISCOURSE_API_USERNAME"]

      have_login_pair = present?(username) && present?(password)
      have_api_pair = present?(api_key) && present?(api_username)
      cli_debug_log(debug_enabled, "auth_start url=#{base_url} preferred=#{preferred_auth || 'none'}")
      cli_debug_log(debug_enabled, "credentials login_pair=#{have_login_pair} api_pair=#{have_api_pair}")

      if preferred_auth.to_s == "login" && !have_login_pair
        prompt = TTY::Prompt.new
        username, password = prompt_for_missing_login_fields(prompt, username, password)
        have_login_pair = present?(username) && present?(password)
        return missing_auth_error unless have_login_pair
        cli_debug_log(debug_enabled, "preferred_login_prompt_complete")
      end

      ui = nil
      auth_order(preferred_auth).each do |method|
        break if ui
        next if method == :login && !have_login_pair
        next if method == :api && !have_api_pair

        cli_debug_log(debug_enabled, "auth_attempt method=#{method}")
        ui = if method == :login
               build_ui_from_login(base_url, username, password, theme_name: theme_name, debug_enabled: debug_enabled, locale: locale)
             else
               build_ui_from_api(base_url, api_key, api_username, theme_name: theme_name, debug_enabled: debug_enabled, locale: locale)
             end
      end

      unless ui || have_login_pair || have_api_pair
        prompt = TTY::Prompt.new
        username, password = prompt_for_missing_login_fields(prompt, username, password)
        have_prompted_login_pair = present?(username) && present?(password)
        return missing_auth_error unless have_prompted_login_pair

        cli_debug_log(debug_enabled, "auth_attempt method=login source=prompt")
        ui = build_ui_from_login(base_url, username, password, prompt: prompt, theme_name: theme_name, debug_enabled: debug_enabled, locale: locale)
      end

      unless ui
        cli_debug_log(debug_enabled, "auth_failed")
        warn t("cli.errors.login_failed")
        return 1
      end

      cli_debug_log(debug_enabled, "auth_success")
      ui.run
      0
    end

    def normalize_base_url(input)
      raw = input.to_s.strip
      return nil if raw.empty?

      candidate = raw.match?(%r{^[a-z][a-z0-9+\-.]*://}i) ? raw : "https://#{raw}"
      uri = URI.parse(candidate)
      return nil if uri.host.to_s.strip.empty?

      "#{uri.scheme}://#{uri.host}"
    rescue URI::InvalidURIError
      nil
    end

    def help_env_variables
      [
        ["DISCOURSE_USERNAME", "Username or email for password login."],
        ["DISCOURSE_PASSWORD", "Password for password login."],
        ["DISCOURSE_API_KEY", "API key for API auth fallback."],
        ["DISCOURSE_API_USERNAME", "Username tied to DISCOURSE_API_KEY."],
        ["TERMCOURSE_CREDENTIALS_FILE", "Credentials YAML path. Lookup order: this path, then ./credentials.yml, then ~/.config/termcourse/credentials.yml."],
        ["TERMCOURSE_HTTP_DEBUG", "Set to 1 to write HTTP/auth debug logs to /tmp/termcourse_http_debug.txt."],
        ["TERMCOURSE_DEBUG", "Set to 1 to write UI render debug logs to /tmp/termcourse_debug.txt."],
        ["TERMCOURSE_LINKS", "Set to 0 to disable clickable links."],
        ["TERMCOURSE_THEME", "Theme name (default|slate|fairground|rust by default)."],
        ["TERMCOURSE_LANG", Localization.t("cli.env.lang")],
        ["TERMCOURSE_COLOR_MODE", "UI color mode: auto|truecolor|256|16 (default: auto; auto uses 256 on macOS)."],
        ["TERMCOURSE_THEME_FILE", "Theme YAML path. Lookup order: this path, then ./theme.yml, then ~/.config/termcourse/theme.yml."],
        ["TERMCOURSE_IMAGES", "Set to 0 to disable inline image previews in expanded posts."],
        ["TERMCOURSE_IMAGE_BACKEND", "Image backend: auto|chafa|viu|off (default: auto)."],
        ["TERMCOURSE_IMAGE_MODE", "Image mode for chafa/viu: stable|quality (default: stable)."],
        ["TERMCOURSE_IMAGE_COLORS", "Chafa quality color mode: auto|none|16|240|256|full (default: auto)."],
        ["TERMCOURSE_IMAGE_LINES", "Target image preview height in terminal lines (default: 14)."],
        ["TERMCOURSE_IMAGE_DEBUG", "Set to 1 to write image renderer debug logs to /tmp/termcourse_image_debug.txt."],
        ["TERMCOURSE_IMAGE_QUALITY_FILTER", "Set to 0 to allow low-quality blocky image previews."],
        ["TERMCOURSE_IMAGE_MAX_BYTES", "Max bytes to download per image preview (default: 5242880)."],
        ["TERMCOURSE_TICK_MS", "UI resize/input poll tick in ms (default: 100)."],
        ["TERMCOURSE_EMOJI", "Set to 0 to disable emoji substitutions."]
      ]
    end

    def auth_order(preferred_auth)
      case preferred_auth.to_s
      when "api"
        [:api]
      when "login"
        [:login]
      else
        [:login, :api]
      end
    end

    def load_site_credentials(base_url)
      path = ENV["TERMCOURSE_CREDENTIALS_FILE"]
      path = default_credentials_path if path.nil? || path.strip.empty?
      return {} unless File.file?(path)

      data = YAML.safe_load(File.read(path)) || {}
      sites = data["sites"]
      return {} unless sites.is_a?(Hash)

      host = URI.parse(base_url).host.to_s.downcase
      entry = sites[host] || sites[host.sub(/^www\./, "")]
      return {} unless entry.is_a?(Hash)

      {
        auth: entry["auth"],
        username: entry["username"],
        password: value_from_entry(entry, "password"),
        api_key: value_from_entry(entry, "api_key"),
        api_username: entry["api_username"]
      }
    rescue StandardError
      {}
    end

    def default_credentials_path
      local = File.expand_path("credentials.yml", Dir.pwd)
      return local if File.file?(local)

      File.expand_path("~/.config/termcourse/credentials.yml")
    end

    def value_from_entry(entry, key)
      value = entry[key]
      env_key = entry["#{key}_env"]
      return ENV[env_key] if (value.nil? || value.to_s.empty?) && env_key && !env_key.to_s.empty?

      value
    end

    def extract_lang_option(argv)
      argv.each_with_index do |arg, idx|
        return arg.split("=", 2).last if arg.start_with?("--lang=")
        return argv[idx + 1] if arg == "--lang"
      end
      nil
    end

    def present?(value)
      !!(value && !value.to_s.strip.empty?)
    end

    def missing_auth_error
      warn t("cli.auth.missing")
      warn t("cli.auth.api")
      warn t("cli.auth.login")
      1
    end

    def prompt_for_missing_login_fields(prompt, username, password)
      if username.nil? || username.strip.empty?
        username = prompt.ask(t("cli.auth.username"))
      end
      if password.nil? || password.strip.empty?
        password = prompt.mask(t("cli.auth.password"))
      end
      [username, password]
    end

    def build_ui_from_api(base_url, api_key, api_username, theme_name: nil, debug_enabled: false, locale: nil)
      client = Client.new(base_url, api_key: api_key, api_username: api_username)
      client.set_debug(debug_enabled)
      current = client.current_user
      return nil unless current.is_a?(Hash) && current["current_user"].is_a?(Hash)

      UI.new(
        base_url,
        client: client,
        api_username: api_username,
        current_user_id: current.dig("current_user", "id"),
        theme_name: theme_name,
        locale: locale
      )
    rescue Faraday::Error
      nil
    end

    def build_ui_from_login(base_url, username, password, prompt: nil, theme_name: nil, debug_enabled: false, locale: nil)
      prompt ||= TTY::Prompt.new
      client = Client.new(base_url)
      client.set_debug(debug_enabled)
      cli_debug_log(debug_enabled, "prompt_complete username=#{username}")

      login = client.login(username: username, password: password)
      if mfa_required?(login)
        method = mfa_method_from(login, prompt)
        otp_label = method == 2 ? t("cli.auth.backup_code") : t("cli.auth.two_factor")
        otp = prompt.ask(otp_label)
        login = client.login(username: username, password: password, otp: otp, otp_method: method)
      end

      current = client.current_user
      login_user = if current.is_a?(Hash) && current["current_user"].is_a?(Hash)
                     current["current_user"]["username"]
                   elsif login.is_a?(Hash)
                     (login.dig("user", "username") || login.dig("current_user", "username") || login["username"])
                   end
      return nil unless login_user

      UI.new(
        base_url,
        client: client,
        api_username: login_user,
        current_user_id: current.is_a?(Hash) ? current.dig("current_user", "id") : nil,
        theme_name: theme_name,
        locale: locale
      )
    rescue Faraday::Error
      nil
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
      options << { label: t("cli.auth.totp"), value: 1 } if login["totp_enabled"]
      options << { label: t("cli.auth.backup"), value: 2 } if login["backup_enabled"]
      return options.first[:value] if options.length == 1

      if options.length > 1
        choice = prompt.select(t("cli.auth.choose_2fa"), options.map { |o| o[:label] })
        selected = options.find { |o| o[:label] == choice }
        return selected[:value] if selected
      end

      1
    end

    def cli_debug_log(enabled, message)
      return unless enabled

      File.open("/tmp/termcourse_http_debug.txt", "a") do |f|
        f.puts("[#{Time.now.utc.iso8601}] #{message}")
      end
    rescue StandardError
      nil
    end

    def t(key, **vars)
      Localization.t(key, locale: @locale, **vars)
    end
  end
end
