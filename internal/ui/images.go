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

const (
	kittyProbeID = 0x5443
	// Keep one virtual placement per image so thumbnail and expanded geometry
	// replace each other instead of leaving ambiguous implicit placements.
	kittyPlacementID = 1
)

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
	rendered, err := u.renderImage(urls[0], backend, columns, rows, true)
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

func (u *UI) renderImage(imageURL, backend string, width, lines int, filterQuality bool) ([]string, error) {
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
		args := chafaArgs(name, width, lines)
		command = exec.Command("chafa", args...)
	}
	if imageMode() != "compat" && imageColors() == "full" {
		command.Env = append(os.Environ(), "COLORTERM=truecolor")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, err
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

func chafaArgs(path string, width, lines int) []string {
	size := fmt.Sprintf("%dx%d", width, lines)
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
		return ok && event.Options.ID == entry.id && event.Options.PlacementID == kittyPlacementID
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
		Action: kitty.TransmitAndPut, ID: imageID, PlacementID: kittyPlacementID, Format: kitty.PNG,
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
	// Unicode placeholders select the image via foreground color and the
	// placement via underline color, without enabling visible underlining.
	prefix := fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[58;2;%d;%d;%dm",
		red, green, blue, byte(kittyPlacementID>>16), byte(kittyPlacementID>>8), byte(kittyPlacementID))
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
renderLoop:
	for {
		width, height := u.terminal.Size()
		if u.kittyAvailable() && u.fullscreenKittyImage(imageURL) {
			return
		}
		backend := imageBackend()
		if backend == "" {
			return
		}
		if backend == "chafa" && u.detectSixel() && u.drawFullscreenSixel(imageURL, width, height) {
			for {
				key, err := u.readKey(u.tick)
				if err != nil || u.leaveImageForKey(key) {
					u.renderer.Reset()
					return
				}
				newWidth, newHeight := u.terminal.Size()
				if key == keyThemeChanged || key == keyHoverChanged || newWidth != width || newHeight != height {
					u.renderer.Reset()
					continue renderLoop
				}
			}
		}
		header := u.imageScreenHeader(width, height)
		screen, pane := u.mainPane(header, width, height, 1)
		rendered, err := u.renderImage(imageURL, backend, max(pane.width, 1), max(pane.height, 1), false)
		if err != nil {
			u.showError(err)
			return
		}
		if len(rendered) == 0 {
			u.showError(fmt.Errorf("image renderer returned no output"))
			return
		}
		u.drawFullscreenImage(screen, pane, rendered, width, height, "fullscreen-image")
		for {
			key, err := u.readKey(u.tick)
			if err != nil || u.leaveImageForKey(key) {
				return
			}
			newWidth, newHeight := u.terminal.Size()
			if key == keyThemeChanged || key == keyHoverChanged || newWidth != width || newHeight != height {
				continue renderLoop
			}
		}
	}
}

func (u *UI) drawFullscreenSixel(imageURL string, width, height int) bool {
	header := u.imageScreenHeader(width, height)
	screen, pane := u.mainPane(header, width, height, 1)
	if pane.width == 0 || pane.height == 0 {
		return false
	}
	cellSize := u.sixelCellSize(width, height)
	maxPixels := image.Pt(pane.width*cellSize.X, pane.height*cellSize.Y)
	// A Sixel band is six pixels tall. Keep even the last partial band inside
	// the pane so it cannot paint over the bottom border.
	maxPixels.Y -= maxPixels.Y % 6
	if maxPixels.X <= 0 || maxPixels.Y <= 0 {
		return false
	}
	payload, err := u.renderNativeImage(imageURL, maxPixels)
	if err != nil {
		u.imageDebug("Fullscreen Sixel encoding failed: %v", err)
		return false
	}
	sequence, ok := placeSixelInPane(payload, pane, cellSize)
	if !ok {
		u.imageDebug("Fullscreen Sixel raster did not fit the pane")
		return false
	}
	if currentWidth, currentHeight := u.terminal.Size(); currentWidth != width || currentHeight != height {
		return false
	}
	u.drawMainPane(screen, pane, nil)
	u.placeControlsFooter(screen, u.t("ui.controls.fullscreen_image"), width)
	u.renderer.Render(screen, width, height, "fullscreen-image-sixel", -1, -1, true)
	if err := u.terminal.rawAfterFrame(sequence); err != nil {
		u.imageDebug("Fullscreen Sixel frame sync failed: %v", err)
		return false
	}
	return true
}

func placeSixelInPane(encoded []byte, pane paneLayout, cellSize image.Point) (string, bool) {
	const dcs, st = "\x1bP", "\x1b\\"
	// Keep just the image. Encoders can add cursor controls or multiple frames
	// that would escape the position set by the application.
	_, afterDCS, found := bytes.Cut(encoded, []byte(dcs))
	if !found {
		return "", false
	}
	frame, _, found := bytes.Cut(afterDCS, []byte(st))
	if !found {
		return "", false
	}
	_, rasterData, found := bytes.Cut(frame, []byte("q"))
	if !found {
		return "", false
	}
	var width, height int
	_, err := fmt.Fscanf(bytes.NewReader(rasterData), "\"1;1;%d;%d", &width, &height)
	if err != nil || width <= 0 || height <= 0 ||
		width > pane.width*cellSize.X || height > pane.height*cellSize.Y/6*6 {
		return "", false
	}
	pixelHeight := (height + 5) / 6 * 6
	col := pane.x + (pane.width*cellSize.X-width)/2/cellSize.X
	row := pane.y + (pane.height*cellSize.Y-pixelHeight)/2/cellSize.Y
	// Reset DEC Sixel display mode so the image starts at the cursor.
	return xansi.SaveCursor + xansi.ResetMode(xansi.DECMode(80)) +
		xansi.CursorPosition(col+1, row+1) + dcs + string(frame) + st + xansi.RestoreCursor, true
}

func (u *UI) fullscreenKittyImage(imageURL string) bool {
	for {
		width, height := u.terminal.Size()
		header := u.imageScreenHeader(width, height)
		screen, pane := u.mainPane(header, width, height, 1)
		rendered, err := u.renderKittyImage(imageURL, max(pane.width, 1), max(pane.height, 1))
		if err != nil || len(rendered) == 0 {
			if err != nil {
				u.reportKittyFailure(imageURL, "fullscreen", err)
			}
			return false
		}
		u.drawFullscreenImage(screen, pane, rendered, width, height, "fullscreen-image-kitty")
		for {
			key, readErr := u.readKey(u.tick)
			if readErr != nil || u.leaveImageForKey(key) {
				return true
			}
			if key == keyThemeChanged || key == keyHoverChanged {
				break
			}
			newWidth, newHeight := u.terminal.Size()
			if newWidth != width || newHeight != height {
				break
			}
		}
	}
}

func (u *UI) drawFullscreenImage(screen []string, pane paneLayout, rendered []string, width, height int, viewKey string) {
	lines := make([]string, pane.height)
	for index := 0; index < len(rendered) && index < len(lines); index++ {
		padding := max((pane.width-visibleWidth(rendered[index]))/2, 0)
		lines[index] = strings.Repeat(" ", padding) + rendered[index]
	}
	u.drawMainPane(screen, pane, lines)
	u.placeControlsFooter(screen, u.t("ui.controls.fullscreen_image"), width)
	// Both backends draw managed cells, including Kitty's Unicode placeholders.
	// A full clear can erase terminal graphics between redraws.
	u.renderer.Render(screen, width, height, viewKey, -1, -1, false)
}

func (u *UI) imageScreenHeader(width, height int) []string {
	return u.navigationHeader(u.t("ui.status.image"), u.activePrimary, "", "", "", nil, width, height)
}

func (u *UI) leaveImageForKey(key string) bool {
	if u.latestRequested {
		return true
	}
	if tab, ok := parsePrimaryTabKey(key); ok {
		if tab == u.activePrimary {
			return false
		}
		u.requestPrimary(tab)
		return true
	}
	return key == "x" || key == "esc"
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

func (u *UI) sixelCellSize(screenWidth, screenHeight int) image.Point {
	// Windows Terminal reports virtual Sixel pixels for the character cell.
	response, _ := u.terminal.Query(xansi.WindowOp(16), func(msg any) bool {
		_, ok := msg.(ultraviolet.CellSizeEvent)
		return ok
	}, 150*time.Millisecond)
	if cell, ok := response.(ultraviolet.CellSizeEvent); ok && cell.Width > 0 && cell.Height > 0 {
		return image.Pt(cell.Width, cell.Height)
	}

	response, _ = u.terminal.Query(xansi.WindowOp(14), func(msg any) bool {
		_, ok := msg.(ultraviolet.PixelSizeEvent)
		return ok
	}, 150*time.Millisecond)
	pixels, ok := response.(ultraviolet.PixelSizeEvent)
	width, height := pixels.Width, pixels.Height
	ok = ok && width > 0 && height > 0
	if !ok {
		graphics, _ := u.terminal.Query("\x1b[?2;1;0S", func(msg any) bool {
			_, ok := msg.(ultraviolet.UnknownCsiEvent)
			return ok
		}, 150*time.Millisecond)
		graphicsResponse, _ := graphics.(ultraviolet.UnknownCsiEvent)
		width, height, ok = parseGraphicsResponse(string(graphicsResponse), "2")
	}
	if !ok {
		return image.Point{}
	}
	return image.Pt(width/max(screenWidth, 1), height/max(screenHeight, 1))
}

func (u *UI) renderNativeImage(imageURL string, maxPixels image.Point) ([]byte, error) {
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
	for _, binary := range []string{"magick", "convert"} {
		if path, lookupErr := exec.LookPath(binary); lookupErr == nil {
			size := fmt.Sprintf("%dx%d", maxPixels.X, maxPixels.Y)
			if output, err := exec.Command(path, name+"[0]", "-resize", size, "sixel:-").Output(); err == nil && len(output) > 0 {
				return output, nil
			}
		}
	}
	path, err := exec.LookPath("chafa")
	if err != nil {
		return nil, err
	}
	// Chafa uses 8×8 output cells when run with piped output and no TTY.
	const chafaCell = 8
	if maxPixels.X < chafaCell || maxPixels.Y < chafaCell {
		return nil, fmt.Errorf("pane is too small for Chafa Sixel output")
	}
	size := fmt.Sprintf("%dx%d", maxPixels.X/chafaCell, maxPixels.Y/chafaCell)
	return exec.Command(path, "--format", "sixels", "--font-ratio", "1", "--scale", "max",
		"--animate", "off", "--polite", "on", "--passthrough", "none",
		"--size", size, "--view-size", size, name).Output()
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
