package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/merefield/termcourse/internal/config"
	"github.com/merefield/termcourse/internal/discourse"
	"github.com/merefield/termcourse/internal/i18n"
	"github.com/merefield/termcourse/internal/theme"
	"github.com/merefield/termcourse/internal/ui"
)

type cliOptions struct {
	apiKey, apiUsername string
	username, password  string
	theme, themeFile    string
	lang, command       string
	baseURL             string
	help                bool
}

func Run(argv []string, stdin *os.File, stdout, stderr io.Writer) int {
	config.LoadDotEnv(".env")
	options, err := parseArgs(argv)
	locale := i18n.ResolveLocale(options.lang)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printHelp(stderr, locale)
		return 1
	}
	if options.help {
		printHelp(stdout, locale)
		return 0
	}
	themeFile := first(options.themeFile, os.Getenv("TERMCOURSE_THEME_FILE"))
	catalog, err := theme.Load(themeFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if options.command == "themes" {
		if options.theme != "" {
			selected, resolveErr := catalog.Resolve(options.theme)
			if resolveErr != nil {
				fmt.Fprintln(stderr, resolveErr)
				return 1
			}
			ui.WriteThemePreviews(stdout, []theme.Theme{selected})
		} else {
			ui.WriteThemePreviews(stdout, catalog.All())
		}
		return 0
	}
	selectedTheme, err := resolveTheme(catalog, options)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	baseURL, err := config.NormalizeBaseURL(options.baseURL)
	if err != nil {
		fmt.Fprintln(stderr, i18n.Tr(locale, "cli.errors.missing_url"))
		printHelp(stderr, locale)
		return 1
	}
	credentials := config.LoadSiteCredentials(baseURL)
	username := first(options.username, credentials.Username, os.Getenv("DISCOURSE_USERNAME"))
	password := first(options.password, credentials.Password, os.Getenv("DISCOURSE_PASSWORD"))
	apiKey := first(options.apiKey, credentials.APIKey, os.Getenv("DISCOURSE_API_KEY"))
	apiUsername := first(options.apiUsername, credentials.APIUsername, os.Getenv("DISCOURSE_API_USERNAME"))
	haveLoginPair := username != "" && password != ""
	haveAPIPair := apiKey != "" && apiUsername != ""
	debug := os.Getenv("TERMCOURSE_HTTP_DEBUG") == "1"

	if credentials.Auth == "login" && (username == "" || password == "") {
		username, password = promptMissingLogin(stdin, stdout, locale, username, password)
		haveLoginPair = username != "" && password != ""
	}

	var client *discourse.Client
	var current discourse.JSON
	loggedInUsername := ""
	enableLive := false
	order := []string{"login", "api"}
	if credentials.Auth == "login" || credentials.Auth == "api" {
		order = []string{credentials.Auth}
	}
	for _, method := range order {
		switch method {
		case "login":
			if username == "" || password == "" {
				continue
			}
			candidate, createErr := discourse.NewClient(baseURL, "", "")
			if createErr != nil {
				continue
			}
			candidate.SetDebug(debug)
			login, loginErr := candidate.Login(username, password, "", 1)
			if loginErr != nil {
				continue
			}
			if mfaRequired(login) {
				methodID := chooseMFAMethod(login, stdin, stdout, locale)
				label := "cli.auth.two_factor"
				if methodID == 2 {
					label = "cli.auth.backup_code"
				}
				code := prompt(stdin, stdout, i18n.Tr(locale, label), false)
				login, loginErr = candidate.Login(username, password, code, methodID)
				if loginErr != nil {
					continue
				}
			}
			currentData, currentErr := candidate.CurrentUser()
			if currentErr != nil {
				continue
			}
			loggedInUsername = nestedUsername(currentData, login)
			if loggedInUsername != "" {
				client, current, enableLive = candidate, currentData, true
			}
		case "api":
			if apiKey == "" || apiUsername == "" {
				continue
			}
			candidate, createErr := discourse.NewClient(baseURL, apiKey, apiUsername)
			if createErr != nil {
				continue
			}
			candidate.SetDebug(debug)
			currentData, currentErr := candidate.CurrentUser()
			if currentErr != nil || discourse.Map(currentData["current_user"]) == nil {
				continue
			}
			client, current, loggedInUsername = candidate, currentData, apiUsername
		}
		if client != nil {
			break
		}
	}

	if client == nil && !haveLoginPair && !haveAPIPair && credentials.Auth != "api" {
		username, password = promptMissingLogin(stdin, stdout, locale, username, password)
		if username != "" && password != "" {
			candidate, _ := discourse.NewClient(baseURL, "", "")
			candidate.SetDebug(debug)
			login, _ := candidate.Login(username, password, "", 1)
			currentData, _ := candidate.CurrentUser()
			loggedInUsername = nestedUsername(currentData, login)
			if loggedInUsername != "" {
				client, current, enableLive = candidate, currentData, true
			}
		}
	}
	if client == nil {
		if username == "" || password == "" {
			fmt.Fprintln(stderr, i18n.Tr(locale, "cli.auth.missing"))
			fmt.Fprintln(stderr, i18n.Tr(locale, "cli.auth.api"))
			fmt.Fprintln(stderr, i18n.Tr(locale, "cli.auth.login"))
		} else {
			fmt.Fprintln(stderr, i18n.Tr(locale, "cli.errors.login_failed"))
		}
		return 1
	}
	user := discourse.Map(current["current_user"])
	var notificationPosition *int
	if raw, present := user["notification_channel_position"]; present {
		value := discourse.Int(raw)
		notificationPosition = &value
	}
	application := ui.New(client, ui.Options{
		BaseURL: baseURL, Username: loggedInUsername, CurrentUserID: discourse.Int(user["id"]),
		NotificationChannelPosition: notificationPosition, Theme: selectedTheme, Themes: catalog.All(), Locale: locale,
		EnableLiveUpdates: enableLive, Input: stdin, Output: stdout,
	})
	if runErr := application.Run(); runErr != nil {
		fmt.Fprintln(stderr, runErr)
		return 1
	}
	return 0
}

func resolveTheme(catalog theme.Catalog, options cliOptions) (theme.Theme, error) {
	return catalog.Resolve(first(options.theme, os.Getenv("TERMCOURSE_THEME")))
}

func parseArgs(argv []string) (cliOptions, error) {
	var result cliOptions
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg == "-h" || arg == "--help" {
			result.help = true
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			if arg == "themes" && result.command == "" && result.baseURL == "" {
				result.command = "themes"
				continue
			}
			if result.command == "themes" {
				if result.theme == "" {
					result.theme = arg
					continue
				}
				return result, fmt.Errorf("unexpected theme argument: %s", arg)
			}
			if result.baseURL != "" {
				return result, fmt.Errorf("unexpected argument: %s", arg)
			}
			result.baseURL = arg
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if !hasValue {
			if index+1 >= len(argv) || strings.HasPrefix(argv[index+1], "-") {
				return result, fmt.Errorf("%s requires a value", name)
			}
			index++
			value = argv[index]
		} else if value == "" {
			return result, fmt.Errorf("%s requires a value", name)
		}
		switch name {
		case "--api-key":
			result.apiKey = value
		case "--api-username":
			result.apiUsername = value
		case "--username":
			result.username = value
		case "--password":
			result.password = value
		case "--theme":
			result.theme = value
		case "--theme-file":
			result.themeFile = value
		case "--lang":
			result.lang = value
		default:
			return result, fmt.Errorf("unknown option: %s", name)
		}
	}
	return result, nil
}

func printHelp(out io.Writer, locale string) {
	fmt.Fprintln(out, i18n.Tr(locale, "cli.usage"))
	fmt.Fprintln(out, i18n.Tr(locale, "cli.usage_themes"))
	options := [][2]string{
		{"--api-key KEY", "cli.help.api_key"}, {"--api-username USER", "cli.help.api_username"},
		{"--username USER", "cli.help.username"}, {"--password PASS", "cli.help.password"},
		{"--theme NAME", "cli.help.theme"}, {"--theme-file PATH", "cli.help.theme_file"},
		{"--lang LANG", "cli.help.lang"}, {"-h, --help", "cli.help.show"},
	}
	for _, option := range options {
		fmt.Fprintf(out, "  %-24s %s\n", option[0], i18n.Tr(locale, option[1]))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, i18n.Tr(locale, "cli.help.core_env"))
	env := [][2]string{
		{"DISCOURSE_USERNAME", "Username or email for password login."},
		{"DISCOURSE_PASSWORD", "Password for password login."},
		{"DISCOURSE_API_KEY", "API key for API auth fallback."},
		{"DISCOURSE_API_USERNAME", "Username tied to DISCOURSE_API_KEY."},
		{"TERMCOURSE_CREDENTIALS_FILE", "Credentials YAML path."},
		{"TERMCOURSE_HTTP_DEBUG", "Set to 1 to enable HTTP/auth debug logs."},
		{"TERMCOURSE_DEBUG", "Set to 1 to enable UI debug logs."},
		{"TERMCOURSE_LINKS", "Set to 0 to disable clickable links."},
		{"TERMCOURSE_MOUSE", "Set to 0 to disable mouse clicks and wheel input."},
		{"TERMCOURSE_THEME", "Theme: default|slate|fairground|rust|hacker."},
		{"TERMCOURSE_LANG", i18n.Tr(locale, "cli.env.lang")},
		{"TERMCOURSE_COLOR_MODE", "UI colors: auto|truecolor|256|16."},
		{"TERMCOURSE_THEME_FILE", "Theme YAML path."},
		{"TERMCOURSE_IMAGES", "Set to 0 to disable image previews."},
		{"TERMCOURSE_IMAGE_PROTOCOL", "Inline images: auto|kitty|symbols."},
		{"TERMCOURSE_IMAGE_BACKEND", "Image backend: auto|chafa|viu|off."},
		{"TERMCOURSE_IMAGE_MODE", "Symbol mode: compat|balanced (default)|high."},
		{"TERMCOURSE_IMAGE_COLORS", "Image colors: auto|none|16|240|256|full."},
		{"TERMCOURSE_IMAGE_COLUMNS", "Image preview width (default 48)."},
		{"TERMCOURSE_IMAGE_LINES", "Image preview height (default 6)."},
		{"TERMCOURSE_IMAGE_DEBUG", "Set to 1 for image diagnostics."},
		{"TERMCOURSE_IMAGE_QUALITY_FILTER", "Set to 0 to allow noisy previews."},
		{"TERMCOURSE_IMAGE_MAX_BYTES", "Image download limit (default 5242880)."},
		{"TERMCOURSE_TICK_MS", "Input/resize poll interval (default 100)."},
		{"TERMCOURSE_EMOJI", "Set to 0 to disable emoji substitutions."},
	}
	for _, item := range env {
		fmt.Fprintf(out, "  %-28s %s\n", item[0], item[1])
	}
}

func promptMissingLogin(in *os.File, out io.Writer, locale, username, password string) (string, string) {
	if username == "" {
		username = prompt(in, out, i18n.Tr(locale, "cli.auth.username"), false)
	}
	if password == "" {
		password = prompt(in, out, i18n.Tr(locale, "cli.auth.password"), true)
	}
	return strings.TrimSpace(username), password
}

func prompt(in *os.File, out io.Writer, label string, masked bool) string {
	fmt.Fprint(out, label, " ")
	if masked && term.IsTerminal(in.Fd()) {
		value, _ := term.ReadPassword(in.Fd())
		fmt.Fprintln(out)
		return strings.TrimSpace(string(value))
	}
	reader := bufio.NewReader(in)
	value, _ := reader.ReadString('\n')
	return strings.TrimSpace(value)
}

func mfaRequired(login discourse.JSON) bool {
	if login == nil {
		return false
	}
	if discourse.Bool(login["second_factor_required"]) || discourse.Bool(login["requires_second_factor"]) {
		return true
	}
	if value := login["second_factor"]; value != nil {
		switch typed := value.(type) {
		case bool:
			if typed {
				return true
			}
		case string:
			if strings.TrimSpace(typed) != "" {
				return true
			}
		case []any:
			if len(typed) > 0 {
				return true
			}
		case map[string]any:
			if len(typed) > 0 {
				return true
			}
		}
	}
	if len(discourse.Slice(login["second_factor_methods"])) > 0 || discourse.String(login["reason"]) == "invalid_second_factor_method" {
		return true
	}
	message := strings.ToLower(discourse.String(login["error"]))
	return strings.Contains(message, "second factor") || strings.Contains(message, "two factor")
}

func chooseMFAMethod(login discourse.JSON, in *os.File, out io.Writer, locale string) int {
	if methods := discourse.Slice(login["second_factor_methods"]); len(methods) > 0 {
		return discourse.Int(methods[0])
	}
	totp, backup := discourse.Bool(login["totp_enabled"]), discourse.Bool(login["backup_enabled"])
	if totp && !backup {
		return 1
	}
	if backup && !totp {
		return 2
	}
	if totp && backup {
		fmt.Fprintf(out, "%s\n  1. %s\n  2. %s\n", i18n.Tr(locale, "cli.auth.choose_2fa"), i18n.Tr(locale, "cli.auth.totp"), i18n.Tr(locale, "cli.auth.backup"))
		value := prompt(in, out, ">", false)
		if number, _ := strconv.Atoi(value); number == 2 {
			return 2
		}
	}
	return 1
}

func nestedUsername(current, login discourse.JSON) string {
	if currentUser := discourse.Map(current["current_user"]); currentUser != nil {
		return discourse.String(currentUser["username"])
	}
	if user := discourse.Map(login["user"]); user != nil {
		return discourse.String(user["username"])
	}
	if user := discourse.Map(login["current_user"]); user != nil {
		return discourse.String(user["username"])
	}
	return discourse.String(login["username"])
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
