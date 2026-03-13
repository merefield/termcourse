# frozen_string_literal: true

require "yaml"

module Termcourse
  module Localization
    AVAILABLE_LOCALES = %w[en fr de es].freeze
    DEFAULT_LOCALE = "en"

    module_function

    def t(key, locale: nil, **vars)
      locale_key = resolve_locale(locale)
      value = lookup(locale_key, key) || lookup(DEFAULT_LOCALE, key) || key.to_s
      interpolate(value, vars)
    end

    def resolve_locale(value = nil)
      raw = value.to_s.strip
      raw = ENV["TERMCOURSE_LANG"].to_s.strip if raw.empty?
      raw = ENV["LC_ALL"].to_s.strip if raw.empty?
      raw = ENV["LC_MESSAGES"].to_s.strip if raw.empty?
      raw = ENV["LANG"].to_s.strip if raw.empty?
      code = raw.downcase[/\A[a-z]{2}/]
      AVAILABLE_LOCALES.include?(code) ? code : DEFAULT_LOCALE
    end

    def reload!
      @translations = nil
    end

    def translations
      @translations ||= load_translations
    end

    def load_translations
      Dir[File.expand_path("../../config/locales/*.yml", __dir__)].each_with_object({}) do |path, memo|
        data = YAML.safe_load(File.read(path)) || {}
        data.each do |locale, values|
          memo[locale.to_s] = deep_stringify_keys(values || {})
        end
      end
    end

    def lookup(locale, key)
      keys = key.to_s.split(".")
      keys.reduce(translations[locale]) do |memo, segment|
        memo.is_a?(Hash) ? memo[segment] : nil
      end
    end

    def interpolate(value, vars)
      return value unless value.is_a?(String)
      return value if vars.empty?

      value.gsub(/%\{([^}]+)\}/) do
        vars.fetch(Regexp.last_match(1).to_sym, Regexp.last_match(0)).to_s
      end
    end

    def deep_stringify_keys(value)
      case value
      when Hash
        value.each_with_object({}) do |(key, nested), memo|
          memo[key.to_s] = deep_stringify_keys(nested)
        end
      when Array
        value.map { |item| deep_stringify_keys(item) }
      else
        value
      end
    end
  end
end
