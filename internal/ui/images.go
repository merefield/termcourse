package ui

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	ultraviolet "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/term"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const kittyProbeID = 0x5443

type kittyInlineImage struct {
	id, columns, rows int
	body              []byte
	placeholders      []string
	failure           error
	reported          bool
}

var (
	markdownImagePattern = regexp.MustCompile(`(?i)!\[[^\]]*\]\(([^)]+)\)`)
	uploadPattern        = regexp.MustCompile(`(?i)upload://[^\s\)]+`)
	uploadsPathPattern   = regexp.MustCompile(`(?i)/uploads/[^\s\)]+`)
	absoluteImagePattern = regexp.MustCompile(`(?i)https?://[^\s\)]+\.(?:png|jpe?g|gif|webp|bmp)(?:\?[^\s\)]*)?`)
	imageExtension       = regexp.MustCompile(`(?i)\.(?:png|jpe?g|gif|webp|bmp)(?:\?.*)?$`)
	da1Pattern           = regexp.MustCompile("\\x1b\\[\\?([\\d;]+)c")
	pixelPattern         = regexp.MustCompile("\\x1b\\[(\\d+);(\\d+);(\\d+)t")
	graphicsPattern      = regexp.MustCompile("\\x1b\\[\\?(\\d+);(\\d+);(\\d+);(\\d+)S")
)

func extractImageURLs(raw, baseURL string) []string {
	var values []string
	for _, match := range markdownImagePattern.FindAllStringSubmatch(raw, -1) {
		values = append(values, resolveImage(match[1], baseURL))
	}
	for _, found := range uploadPattern.FindAllString(raw, -1) {
		values = append(values, resolveImage(found, baseURL))
	}
	for _, found := range uploadsPathPattern.FindAllString(raw, -1) {
		values = append(values, resolveImage(found, baseURL))
	}
	values = append(values, absoluteImagePattern.FindAllString(raw, -1)...)
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimRight(strings.TrimSpace(value), `>"'`)
		if value != "" && imageExtension.MatchString(value) && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func resolveImage(value, baseURL string) string {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "upload://"):
		return strings.TrimRight(baseURL, "/") + "/uploads/short-url/" + strings.TrimPrefix(value, "upload://")
	case strings.HasPrefix(value, "/uploads/"):
		return strings.TrimRight(baseURL, "/") + value
	case strings.HasPrefix(value, "http://"), strings.HasPrefix(value, "https://"):
		return value
	default:
		return ""
	}
}

func stripMarkdownImages(raw string) string {
	return markdownImagePattern.ReplaceAllString(raw, "")
}

func (u *UI) imagePreview(raw string, width int) []string {
	if os.Getenv("TERMCOURSE_IMAGES") == "0" {
		return nil
	}
	urls := extractImageURLs(raw, u.options.BaseURL)
	if len(urls) == 0 {
		return nil
	}
	columns := min(width, envInt("TERMCOURSE_IMAGE_COLUMNS", 48))
	rows := envInt("TERMCOURSE_IMAGE_LINES", 6)
	if u.kittyAvailable() {
		if rendered, err := u.renderKittyImage(urls[0], columns, rows); err == nil && len(rendered) > 0 {
			return append([]string{fitCell(u.t("ui.posts.image"), width, false)}, rendered...)
		} else if err != nil {
			u.reportKittyFailure(urls[0], "inline", err)
		}
	}
	backend := imageBackend()
	if backend == "" {
		return nil
	}
	key := strings.Join([]string{urls[0], backend, imageMode(), imageColors(), strconv.Itoa(width), strconv.Itoa(columns), strconv.Itoa(rows)}, "|")
	if rendered, present := u.imageCache[key]; present {
		return append([]string{}, rendered...)
	}
	rendered, err := u.renderImage(urls[0], backend, columns, rows, false, true)
	if err != nil || len(rendered) == 0 {
		u.imageCache[key] = nil
		return nil
	}
	rendered = append([]string{fitCell(u.t("ui.posts.image"), width, false)}, rendered...)
	u.imageCache[key] = append([]string{}, rendered...)
	return rendered
}

func imageBackend() string {
	preference := strings.ToLower(os.Getenv("TERMCOURSE_IMAGE_BACKEND"))
	if preference == "off" || preference == "none" || preference == "0" {
		return ""
	}
	if preference == "chafa" || preference == "viu" {
		if _, err := exec.LookPath(preference); err == nil {
			return preference
		}
		return ""
	}
	if _, err := exec.LookPath("chafa"); err == nil {
		return "chafa"
	}
	if _, err := exec.LookPath("viu"); err == nil {
		return "viu"
	}
	return ""
}

func (u *UI) renderImage(imageURL, backend string, width, lines int, sixel, filterQuality bool) ([]string, error) {
	body, err := u.client.GetBytes(imageURL, envInt("TERMCOURSE_IMAGE_MAX_BYTES", 5_242_880))
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(imageURL)
	ext := filepath.Ext(parsed.Path)
	file, err := os.CreateTemp("", "termcourse-image-*"+ext)
	if err != nil {
		return nil, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(body); err != nil {
		file.Close()
		return nil, err
	}
	_ = file.Close()

	var command *exec.Cmd
	if backend == "viu" {
		command = exec.Command("viu", "-h", strconv.Itoa(lines), "--blocks", "--transparent", name)
	} else {
		args := chafaArgs(name, width, lines, sixel)
		command = exec.Command("chafa", args...)
	}
	if imageMode() != "compat" && imageColors() == "full" {
		command.Env = append(os.Environ(), "COLORTERM=truecolor")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, err
	}
	if sixel {
		return []string{string(output)}, nil
	}
	var rendered []string
	preserve := backend == "viu" || imageMode() != "compat"
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r", ""), "\n") {
		if line == "" {
			continue
		}
		if !preserve {
			line = stripANSI(line)
		}
		line = truncateVisible(line, width)
		if strings.TrimSpace(stripANSI(line)) != "" {
			rendered = append(rendered, line)
		}
	}
	if filterQuality && os.Getenv("TERMCOURSE_IMAGE_QUALITY_FILTER") != "0" && backend == "chafa" && imageMode() == "compat" && lowQuality(rendered) {
		return nil, nil
	}
	return rendered, nil
}

func chafaArgs(path string, width, lines int, sixel bool) []string {
	size := fmt.Sprintf("%dx%d", width, lines)
	if sixel {
		return []string{"--format", "sixels", "--scale", "max", "--align", "top,left", "--margin-bottom", "0", "--optimize", "9", "--size", size, "--view-size", size, path}
	}
	switch imageMode() {
	case "balanced":
		return []string{"--format", "symbols", "--symbols", "vhalf", "--colors", imageColors(), "--optimize", "5", "--work", "5", "--size", size, path}
	case "high":
		return []string{"--format", "symbols", "--symbols", "vhalf+block", "--colors", imageColors(), "--optimize", "9", "--work", "9", "--size", size, path}
	default:
		return []string{"--format", "symbols", "--symbols", "ascii", "--colors", "none", "--optimize", "0", "--work", "1", "--size", size, path}
	}
}

func imageMode() string {
	mode := strings.ToLower(os.Getenv("TERMCOURSE_IMAGE_MODE"))
	if mode == "compat" || mode == "balanced" || mode == "high" {
		return mode
	}
	return "balanced"
}

func imageColors() string {
	value := strings.ToLower(os.Getenv("TERMCOURSE_IMAGE_COLORS"))
	for _, allowed := range []string{"none", "16", "240", "256", "full"} {
		if value == allowed {
			return value
		}
	}
	if strings.Contains(strings.ToLower(os.Getenv("COLORTERM")), "truecolor") {
		return "full"
	}
	if strings.Contains(strings.ToLower(os.Getenv("TERM")), "256color") {
		return "256"
	}
	return "16"
}

func (u *UI) kittyAvailable() bool {
	if os.Getenv("TERMCOURSE_IMAGES") == "0" || u.style == nil || u.style.ColorMode != "truecolor" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TERMCOURSE_IMAGE_PROTOCOL"))) {
	case "symbols", "text", "off":
		return false
	case "kitty":
		return true
	}
	if u.kittyChecked {
		return u.kittySupported
	}
	u.kittyChecked = true
	if u.terminal == nil || u.terminal.program == nil {
		return false
	}

	probe := kitty.Options{
		Action: kitty.Query, ID: kittyProbeID, Format: kitty.RGB,
		ImageWidth: 1, ImageHeight: 1, Transmission: kitty.Direct,
	}
	sequence := u.graphicsPassthrough(xansi.KittyGraphics([]byte("AAAA"), probe.Options()...)) + xansi.RequestPrimaryDeviceAttributes
	response, err := u.terminal.Query(sequence, func(msg any) bool {
		switch event := msg.(type) {
		case ultraviolet.KittyGraphicsEvent:
			return event.Options.ID == kittyProbeID
		case ultraviolet.PrimaryDeviceAttributesEvent:
			return true
		default:
			return false
		}
	}, 250*time.Millisecond)
	if event, ok := response.(ultraviolet.KittyGraphicsEvent); err == nil && ok {
		u.kittySupported = strings.HasPrefix(strings.ToUpper(string(event.Payload)), "OK")
	}
	return u.kittySupported
}

func (u *UI) graphicsPassthrough(sequence string) string {
	if os.Getenv("TMUX") != "" {
		return xansi.TmuxPassthrough(sequence)
	}
	if os.Getenv("STY") != "" {
		return xansi.ScreenPassthrough(sequence, 768)
	}
	return sequence
}

func (u *UI) kittyCellSize() (int, int) {
	if u.cellSizeChecked {
		return u.cellWidthPixels, u.cellHeightPixels
	}
	u.cellSizeChecked = true
	u.cellWidthPixels, u.cellHeightPixels = 8, 16
	if u.terminal == nil || u.terminal.program == nil {
		return u.cellWidthPixels, u.cellHeightPixels
	}
	response, err := u.terminal.Query(xansi.WindowOp(16), func(msg any) bool {
		_, ok := msg.(ultraviolet.CellSizeEvent)
		return ok
	}, 150*time.Millisecond)
	if cell, ok := response.(ultraviolet.CellSizeEvent); err == nil && ok && cell.Width > 0 && cell.Height > 0 {
		u.cellWidthPixels, u.cellHeightPixels = cell.Width, cell.Height
	}
	return u.cellWidthPixels, u.cellHeightPixels
}

func (u *UI) renderKittyImage(imageURL string, maxColumns, maxRows int) ([]string, error) {
	if u.terminal == nil || u.terminal.program == nil {
		return nil, fmt.Errorf("terminal program is not running")
	}
	if u.kittyImages == nil {
		u.kittyImages = map[string]*kittyInlineImage{}
	}
	maxColumns, maxRows = max(maxColumns, 1), max(maxRows, 1)
	entry := u.kittyImages[imageURL]
	if entry == nil {
		body, err := u.client.GetBytes(imageURL, envInt("TERMCOURSE_IMAGE_MAX_BYTES", 5_242_880))
		if err != nil {
			return nil, err
		}
		u.evictKittyImage()
		entry = &kittyInlineImage{id: u.kittyImageID(imageURL), body: body}
		u.kittyImages[imageURL] = entry
		u.kittyOrder = append(u.kittyOrder, imageURL)
	}
	if entry.failure != nil {
		return nil, entry.failure
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(entry.body))
	if err != nil {
		entry.failure = fmt.Errorf("decode image dimensions: %w", err)
		return nil, entry.failure
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		entry.failure = fmt.Errorf("image dimensions %dx%d exceed the inline limit", config.Width, config.Height)
		return nil, entry.failure
	}
	cellWidth, cellHeight := u.kittyCellSize()
	columns, rows := kittyCellGeometry(config.Width, config.Height, maxColumns, maxRows, cellWidth, cellHeight)
	if entry.columns == columns && entry.rows == rows && len(entry.placeholders) > 0 {
		return append([]string{}, entry.placeholders...), nil
	}

	source, _, err := image.Decode(bytes.NewReader(entry.body))
	if err != nil {
		entry.failure = fmt.Errorf("decode image: %w", err)
		return nil, entry.failure
	}
	targetWidth, targetHeight := boundedPixelSize(columns*cellWidth, rows*cellHeight, 12_000_000)
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(target, target.Bounds(), source, source.Bounds(), draw.Over, nil)

	encoded, err := encodeKittyPlacement(target, entry.id, columns, rows, u.graphicsPassthrough)
	if err != nil {
		entry.failure = err
		return nil, entry.failure
	}
	placeholders := kittyPlaceholderLines(entry.id, columns, rows)
	if len(placeholders) == 0 {
		return nil, fmt.Errorf("create Kitty image placeholders")
	}
	response, err := u.terminal.Query(encoded, func(msg any) bool {
		event, ok := msg.(ultraviolet.KittyGraphicsEvent)
		return ok && event.Options.ID == entry.id
	}, 750*time.Millisecond)
	if err != nil {
		entry.failure = fmt.Errorf("Kitty placement acknowledgement: %w", err)
		return nil, entry.failure
	}
	event := response.(ultraviolet.KittyGraphicsEvent)
	if !strings.HasPrefix(strings.ToUpper(string(event.Payload)), "OK") {
		entry.failure = fmt.Errorf("Kitty placement rejected: %s", strings.TrimSpace(string(event.Payload)))
		return nil, entry.failure
	}
	entry.columns, entry.rows = columns, rows
	entry.placeholders = append([]string{}, placeholders...)
	return placeholders, nil
}

func encodeKittyPlacement(source image.Image, imageID, columns, rows int, passthrough func(string) string) (string, error) {
	var encoded bytes.Buffer
	options := &kitty.Options{
		Action: kitty.TransmitAndPut, ID: imageID, Format: kitty.PNG,
		Transmission: kitty.Direct, Chunk: true,
		Columns: columns, Rows: rows, VirtualPlacement: true,
		ChunkFormatter: passthrough,
	}
	if err := kitty.EncodeGraphics(&encoded, source, options); err != nil {
		return "", fmt.Errorf("encode Kitty image: %w", err)
	}
	return encoded.String(), nil
}

func (u *UI) kittyImageID(imageURL string) int {
	if u.kittyIDs == nil {
		u.kittyIDs = map[int]string{}
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(imageURL))
	id := int(hash.Sum32() & 0x00ffffff)
	if id == 0 {
		id = 1
	}
	for {
		if owner, present := u.kittyIDs[id]; !present || owner == imageURL {
			u.kittyIDs[id] = imageURL
			return id
		}
		id = id%0x00ffffff + 1
	}
}

func kittyCellGeometry(imageWidth, imageHeight, maxColumns, maxRows, cellWidth, cellHeight int) (int, int) {
	// Kitty's row/column diacritic table is finite. Staying within 255 cells
	// keeps every placeholder explicit and avoids relying on inheritance.
	maxColumns, maxRows = min(max(maxColumns, 1), 255), min(max(maxRows, 1), 255)
	cellWidth, cellHeight = max(cellWidth, 1), max(cellHeight, 1)
	if imageWidth <= 0 || imageHeight <= 0 {
		return 1, 1
	}
	columns := maxColumns
	rows := max(int(float64(columns*cellWidth)/float64(imageWidth)*float64(imageHeight)/float64(cellHeight)+0.5), 1)
	if rows > maxRows {
		rows = maxRows
		columns = max(int(float64(rows*cellHeight)/float64(imageHeight)*float64(imageWidth)/float64(cellWidth)+0.5), 1)
	}
	return min(columns, maxColumns), min(rows, maxRows)
}

func boundedPixelSize(width, height int, maxPixels int64) (int, int) {
	width, height = max(width, 1), max(height, 1)
	area := int64(width) * int64(height)
	if maxPixels <= 0 || area <= maxPixels {
		return width, height
	}
	scale := math.Sqrt(float64(maxPixels) / float64(area))
	return max(int(float64(width)*scale), 1), max(int(float64(height)*scale), 1)
}

func kittyPlaceholderLines(imageID, columns, rows int) []string {
	if imageID <= 0 || columns <= 0 || rows <= 0 {
		return nil
	}
	red, green, blue := byte(imageID>>16), byte(imageID>>8), byte(imageID)
	prefix := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", red, green, blue)
	lines := make([]string, rows)
	for row := range rows {
		var line strings.Builder
		line.WriteString(prefix)
		for column := range columns {
			line.WriteRune(kitty.Placeholder)
			line.WriteRune(kitty.Diacritic(row))
			line.WriteRune(kitty.Diacritic(column))
		}
		line.WriteString(xansi.ResetStyle)
		lines[row] = line.String()
	}
	return lines
}

func (u *UI) clearKittyImages() {
	if !u.kittySupported && strings.ToLower(strings.TrimSpace(os.Getenv("TERMCOURSE_IMAGE_PROTOCOL"))) != "kitty" {
		return
	}
	var sequence strings.Builder
	for _, entry := range u.kittyImages {
		options := kitty.Options{
			Action: kitty.Delete, Quiet: 2, ID: entry.id,
			Delete: kitty.DeleteID, DeleteResources: true,
		}
		sequence.WriteString(u.graphicsPassthrough(xansi.KittyGraphics(nil, options.Options()...)))
	}
	if sequence.Len() > 0 {
		u.terminal.Raw(sequence.String())
	}
}

func (u *UI) reportKittyFailure(imageURL, context string, err error) {
	entry := u.kittyImages[imageURL]
	if entry != nil && entry.reported {
		return
	}
	u.imageDebug("Kitty %s fallback for %s: %v", context, imageURL, err)
	if entry != nil {
		entry.reported = true
	}
}

func (u *UI) evictKittyImage() {
	const maxCachedKittyImages = 8
	if len(u.kittyOrder) < maxCachedKittyImages {
		return
	}
	imageURL := u.kittyOrder[0]
	u.kittyOrder = u.kittyOrder[1:]
	entry := u.kittyImages[imageURL]
	delete(u.kittyImages, imageURL)
	if entry == nil {
		return
	}
	delete(u.kittyIDs, entry.id)
	options := kitty.Options{
		Action: kitty.Delete, Quiet: 2, ID: entry.id,
		Delete: kitty.DeleteID, DeleteResources: true,
	}
	u.terminal.Raw(u.graphicsPassthrough(xansi.KittyGraphics(nil, options.Options()...)))
}

func (u *UI) fullscreenImage(imageURL string) {
	width, height := u.terminal.Size()
	if u.kittyAvailable() && u.fullscreenKittyImage(imageURL) {
		return
	}
	backend := imageBackend()
	if backend == "" {
		return
	}
	if backend == "chafa" && u.detectSixel() {
		if payload, nativeErr := u.renderNativeImage(imageURL, width, max(height-1, 1), u.nativePixelViewport(height)); nativeErr == nil && len(payload) > 0 {
			// Keep Bubble Tea's current model intact while the raw Sixel payload
			// temporarily owns the screen, then invalidate it on exit so the topic
			// is fully repainted without covering the image while it is open.
			defer u.renderer.Reset()
			footer := padLine(u.style.Text(u.t("ui.controls.fullscreen_image"), roleListMeta), width)
			u.terminal.Raw(xansi.HideCursor + xansi.EraseEntireScreen + xansi.CursorPosition(1, 1) + string(payload) + xansi.CursorPosition(1, height) + xansi.ResetStyle + xansi.EraseEntireLine + footer + xansi.HideCursor)
			for {
				key, err := u.terminal.ReadKey(u.tick)
				if err != nil || key == "x" || key == "esc" {
					return
				}
			}
		}
	}
	rendered, err := u.renderImage(imageURL, backend, width, max(height-1, 1), false, false)
	if err != nil {
		u.showError(err)
		return
	}
	if len(rendered) == 0 {
		u.showError(fmt.Errorf("image renderer returned no output"))
		return
	}
	screen := make([]string, height)
	for index := 0; index < len(rendered) && index < height-1; index++ {
		padding := max((width-visibleWidth(rendered[index]))/2, 0)
		screen[index] = strings.Repeat(" ", padding) + rendered[index]
	}
	screen[height-1] = headerLine(u.t("ui.controls.fullscreen_image"), u.displayURL, width)
	u.renderer.Render(screen, width, height, "fullscreen-image", -1, -1, true)
	for {
		key, err := u.terminal.ReadKey(u.tick)
		if err != nil || key == "x" || key == "esc" {
			return
		}
	}
}

func (u *UI) fullscreenKittyImage(imageURL string) bool {
	for {
		width, height := u.terminal.Size()
		rendered, err := u.renderKittyImage(imageURL, width, max(height-1, 1))
		if err != nil || len(rendered) == 0 {
			if err != nil {
				u.reportKittyFailure(imageURL, "fullscreen", err)
			}
			return false
		}
		screen := make([]string, height)
		for index := 0; index < len(rendered) && index < height-1; index++ {
			padding := max((width-visibleWidth(rendered[index]))/2, 0)
			screen[index] = strings.Repeat(" ", padding) + rendered[index]
		}
		screen[height-1] = headerLine(u.t("ui.controls.fullscreen_image"), u.displayURL, width)
		u.renderer.Render(screen, width, height, "fullscreen-image-kitty", -1, -1, true)
		for {
			key, readErr := u.terminal.ReadKey(u.tick)
			if readErr != nil || key == "x" || key == "esc" {
				return true
			}
			newWidth, newHeight := u.terminal.Size()
			if newWidth != width || newHeight != height {
				break
			}
		}
	}
}

func (u *UI) detectSixel() bool {
	if _, err := exec.LookPath("chafa"); err != nil || !term.IsTerminal(u.options.Input.Fd()) {
		return false
	}
	response, err := u.terminal.Query(xansi.RequestPrimaryDeviceAttributes, func(msg any) bool {
		_, ok := msg.(ultraviolet.PrimaryDeviceAttributesEvent)
		return ok
	}, 150*time.Millisecond)
	if err != nil {
		return false
	}
	for _, capability := range response.(ultraviolet.PrimaryDeviceAttributesEvent) {
		if capability == 4 {
			return true
		}
	}
	return false
}

func parseDA1Sixel(response string) bool {
	for _, match := range da1Pattern.FindAllStringSubmatch(response, -1) {
		for _, capability := range strings.Split(match[1], ";") {
			if capability == "4" {
				return true
			}
		}
	}
	return false
}

func parsePixelResponse(response, expectedCode string) (int, int, bool) {
	match := pixelPattern.FindStringSubmatch(response)
	if len(match) != 4 || match[1] != expectedCode {
		return 0, 0, false
	}
	height, _ := strconv.Atoi(match[2])
	width, _ := strconv.Atoi(match[3])
	return width, height, width > 0 && height > 0
}

func parseGraphicsResponse(response, expectedItem string) (int, int, bool) {
	match := graphicsPattern.FindStringSubmatch(response)
	if len(match) != 5 || match[1] != expectedItem || match[2] != "0" {
		return 0, 0, false
	}
	width, _ := strconv.Atoi(match[3])
	height, _ := strconv.Atoi(match[4])
	return width, height, width > 0 && height > 0
}

func (u *UI) nativePixelViewport(screenHeight int) *[2]int {
	graphics, _ := u.terminal.Query("\x1b[?2;1;0S", func(msg any) bool {
		_, ok := msg.(ultraviolet.UnknownCsiEvent)
		return ok
	}, 150*time.Millisecond)
	graphicsResponse, _ := graphics.(ultraviolet.UnknownCsiEvent)
	width, height, ok := parseGraphicsResponse(string(graphicsResponse), "2")
	if !ok {
		response, _ := u.terminal.Query(xansi.WindowOp(14), func(msg any) bool {
			_, ok := msg.(ultraviolet.PixelSizeEvent)
			return ok
		}, 150*time.Millisecond)
		if pixels, responseOK := response.(ultraviolet.PixelSizeEvent); responseOK {
			width, height, ok = pixels.Width, pixels.Height, pixels.Width > 0 && pixels.Height > 0
		}
	}
	if !ok {
		return nil
	}
	cellResponse, _ := u.terminal.Query(xansi.WindowOp(16), func(msg any) bool {
		_, ok := msg.(ultraviolet.CellSizeEvent)
		return ok
	}, 150*time.Millisecond)
	cell, cellOK := cellResponse.(ultraviolet.CellSizeEvent)
	cellHeight := cell.Height
	if !cellOK || cellHeight <= 0 {
		cellHeight = max(int(float64(height)/float64(max(screenHeight, 1))+0.5), 1)
	}
	viewport := [2]int{width, max(height-cellHeight, 1)}
	return &viewport
}

func (u *UI) renderNativeImage(imageURL string, width, lines int, viewport *[2]int) ([]byte, error) {
	body, err := u.client.GetBytes(imageURL, envInt("TERMCOURSE_IMAGE_MAX_BYTES", 5_242_880))
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(imageURL)
	file, err := os.CreateTemp("", "termcourse-native-*"+filepath.Ext(parsed.Path))
	if err != nil {
		return nil, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return nil, err
	}
	_ = file.Close()
	if viewport != nil {
		for _, binary := range []string{"magick", "convert"} {
			if path, lookupErr := exec.LookPath(binary); lookupErr == nil {
				return exec.Command(path, name, "-resize", fmt.Sprintf("%dx%d", viewport[0], viewport[1]), "sixel:-").Output()
			}
		}
	}
	path, err := exec.LookPath("chafa")
	if err != nil {
		return nil, err
	}
	return exec.Command(path, chafaArgs(name, width, lines, true)...).Output()
}

func lowQuality(lines []string) bool {
	text := strings.Join(lines, "")
	if text == "" {
		return true
	}
	block := 0
	unique := map[rune]bool{}
	for _, r := range text {
		unique[r] = true
		if strings.ContainsRune("█▀▄▌▐▍▎▏▁▂▃▅▆▇░▒▓", r) {
			block++
		}
	}
	return float64(block)/float64(len([]rune(text))) > 0.55 && len(unique) <= 8
}

func envInt(name string, fallback int) int {
	value, _ := strconv.Atoi(os.Getenv(name))
	if value <= 0 {
		return fallback
	}
	return value
}

func (u *UI) imageDebug(format string, args ...any) {
	if os.Getenv("TERMCOURSE_IMAGE_DEBUG") != "1" {
		return
	}
	path := filepath.Join(os.TempDir(), "termcourse_image_debug.txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		_, _ = fmt.Fprintf(file, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
		_ = file.Close()
	}
}
