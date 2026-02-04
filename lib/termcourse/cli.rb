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
        api_username: ENV["DISCOURSE_API_USERNAME"]
      }

      parser = OptionParser.new do |opts|
        opts.banner = "Usage: termcourse [options] <discourse_url>"

        opts.on("--api-key KEY", "Discourse API key (or DISCOURSE_API_KEY)") do |value|
          options[:api_key] = value
        end

        opts.on("--api-username USER", "Discourse API username (or DISCOURSE_API_USERNAME)") do |value|
          options[:api_username] = value
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

      if options[:api_key].nil? || options[:api_key].strip.empty?
        warn "Missing API key. Set DISCOURSE_API_KEY or --api-key."
        return 1
      end

      if options[:api_username].nil? || options[:api_username].strip.empty?
        warn "Missing API username. Set DISCOURSE_API_USERNAME or --api-username."
        return 1
      end

      ui = UI.new(base_url, api_key: options[:api_key], api_username: options[:api_username])
      ui.run
      0
    end
  end
end
