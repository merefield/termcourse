# frozen_string_literal: true

require_relative "test_helper"

module Termcourse
  class CLITest < Minitest::Test
    def test_help_honors_lang_override_before_option_parsing_finishes
      stdout, = capture_io do
        result = CLI.new(["--lang", "fr", "--help"]).run
        assert_equal 0, result
      end

      assert_includes stdout, "Utilisation"
      assert_includes stdout, "Variables d’environnement principales"
    end

    def test_missing_url_honors_lang_override
      _, stderr = capture_io do
        result = CLI.new(["--lang", "fr"]).run
        assert_equal 1, result
      end

      assert_includes stderr, "discourse_url manquant"
      assert_includes stderr, "Utilisation"
    end
  end
end
