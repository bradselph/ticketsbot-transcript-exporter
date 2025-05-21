package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cheggaaa/pb/v3"
)

type Config struct {
	XTicketsHeader         string
	AuthHeader             string
	ServerID               string
	StartID                int
	EndID                  int
	OutputDir              string
	Formats                []string
	RateLimitDelay         float64
	MaxConsecutiveFailures int
	MaxRetries             int
	RetryDelay             float64
	Concurrency            int
	Resume                 bool
	Verbose                bool
}

type State struct {
	StartID      int   `json:"start_id"`
	EndID        int   `json:"end_id"`
	CompletedIDs []int `json:"completed_ids"`
	Timestamp    int64 `json:"timestamp"`
}

type Transcript struct {
	Success     bool                   `json:"success"`
	Error       string                 `json:"error,omitempty"`
	Messages    []Message              `json:"messages"`
	ChannelName string                 `json:"channel_name"`
	Entities    map[string]interface{} `json:"entities"`
}

type Message struct {
	Author      string       `json:"author"`
	Content     string       `json:"content"`
	Time        int64        `json:"time"`
	Embeds      []Embed      `json:"embeds"`
	Attachments []Attachment `json:"attachments"`
}

type Embed struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}

type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Attachment struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

type User struct {
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

type Logger struct {
	file    *os.File
	verbose bool
}

func NewLogger(outputDir string, verbose bool) (*Logger, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	logFile, err := os.OpenFile(
		filepath.Join(outputDir, "transcript_export.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return &Logger{
		file:    logFile,
		verbose: verbose,
	}, nil
}

func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) Info(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("%s - INFO - %s\n", timestamp, message)
	fmt.Fprintf(l.file, "%s - INFO - %s\n", timestamp, message)
}

func (l *Logger) Warning(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("%s - WARNING - %s\n", timestamp, message)
	fmt.Fprintf(l.file, "%s - WARNING - %s\n", timestamp, message)
}

func (l *Logger) Error(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("%s - ERROR - %s\n", timestamp, message)
	fmt.Fprintf(l.file, "%s - ERROR - %s\n", timestamp, message)
}

func (l *Logger) Critical(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("%s - CRITICAL - %s\n", timestamp, message)
	fmt.Fprintf(l.file, "%s - CRITICAL - %s\n", timestamp, message)
}

func (l *Logger) Debug(format string, args ...interface{}) {
	if !l.verbose {
		return
	}
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("%s - DEBUG - %s\n", timestamp, message)
	fmt.Fprintf(l.file, "%s - DEBUG - %s\n", timestamp, message)
}

type TranscriptExporter struct {
	config       Config
	logger       *Logger
	completedIDs map[int]bool
	mutex        sync.Mutex
}

func NewTranscriptExporter(config Config, logger *Logger) *TranscriptExporter {
	return &TranscriptExporter{
		config:       config,
		logger:       logger,
		completedIDs: make(map[int]bool),
		mutex:        sync.Mutex{},
	}
}

func waitForEnter(message string) {
	fmt.Println(message)
	reader := bufio.NewReader(os.Stdin)
	reader.ReadString('\n')
}

func getInput(prompt string, defaultValue string) string {
	reader := bufio.NewReader(os.Stdin)
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Printf("%s: ", prompt)
	}

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return defaultValue
	}

	return input
}

func validateFormats(formats []string) error {
	validFormats := map[string]bool{
		"JSON":     true,
		"TXT":      true,
		"HTML":     true,
		"CSV":      true,
		"MD":       true,
		"MARKDOWN": true,
	}

	for _, format := range formats {
		if !validFormats[strings.ToUpper(format)] {
			return fmt.Errorf("invalid format: %s", format)
		}
	}

	return nil
}

func exitWithError(message string) {
	fmt.Printf("Error: %s\n", message)
	waitForEnter("Press Enter to exit...")
	os.Exit(1)
}

func runInteractiveMode() (*Config, error) {
	fmt.Println("TicketsBot Transcript Exporter")
	fmt.Println("-----------------------------")
	fmt.Println("This program will help you export transcripts from the TicketsBot API.")
	fmt.Println("")

	xTicketsHeader := getInput("Enter the x-tickets header value (required)", "")
	if xTicketsHeader == "" {
		return nil, fmt.Errorf("x-tickets header is required")
	}

	authHeader := getInput("Enter the authorization header value (required)", "")
	if authHeader == "" {
		return nil, fmt.Errorf("authorization header is required")
	}

	serverID := getInput("Enter your server ID (required)", "")
	if serverID == "" {
		return nil, fmt.Errorf("server ID is required")
	}

	startIDStr := getInput("Enter starting transcript ID", "1")
	startID, err := strconv.Atoi(startIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid start ID: %w", err)
	}

	endIDStr := getInput("Enter ending transcript ID", "2500")
	endID, err := strconv.Atoi(endIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid end ID: %w", err)
	}

	outputDir := getInput("Enter output directory", "transcripts")

	formatsStr := getInput("Enter output formats (JSON,TXT,HTML,CSV,MD)", "JSON,TXT,HTML")
	formats := strings.Split(formatsStr, ",")
	for i, format := range formats {
		formats[i] = strings.TrimSpace(format)
	}

	rateLimitDelayStr := getInput("Enter delay between requests in seconds", "1.0")
	rateLimitDelay, err := strconv.ParseFloat(rateLimitDelayStr, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid rate limit delay: %w", err)
	}

	maxConsecutiveFailuresStr := getInput("Enter maximum consecutive failures", "10")
	maxConsecutiveFailures, err := strconv.Atoi(maxConsecutiveFailuresStr)
	if err != nil {
		return nil, fmt.Errorf("invalid maximum consecutive failures: %w", err)
	}

	maxRetriesStr := getInput("Enter maximum retries per transcript", "3")
	maxRetries, err := strconv.Atoi(maxRetriesStr)
	if err != nil {
		return nil, fmt.Errorf("invalid maximum retries: %w", err)
	}

	retryDelayStr := getInput("Enter delay between retries in seconds", "2.0")
	retryDelay, err := strconv.ParseFloat(retryDelayStr, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid retry delay: %w", err)
	}

	concurrencyStr := getInput("Enter number of concurrent requests", "1")
	concurrency, err := strconv.Atoi(concurrencyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid concurrency: %w", err)
	}

	resumeStr := getInput("Resume from previous export? (y/n)", "n")
	resume := strings.ToLower(resumeStr) == "y" || strings.ToLower(resumeStr) == "yes"

	verboseStr := getInput("Enable verbose logging? (y/n)", "n")
	verbose := strings.ToLower(verboseStr) == "y" || strings.ToLower(verboseStr) == "yes"

	if err := validateFormats(formats); err != nil {
		return nil, err
	}

	config := &Config{
		XTicketsHeader:         xTicketsHeader,
		AuthHeader:             authHeader,
		ServerID:               serverID,
		StartID:                startID,
		EndID:                  endID,
		OutputDir:              outputDir,
		Formats:                formats,
		RateLimitDelay:         rateLimitDelay,
		MaxConsecutiveFailures: maxConsecutiveFailures,
		MaxRetries:             maxRetries,
		RetryDelay:             retryDelay,
		Concurrency:            concurrency,
		Resume:                 resume,
		Verbose:                verbose,
	}

	fmt.Println("")
	fmt.Println("Ready to export with the following configuration:")
	fmt.Printf("Server ID: %s\n", config.ServerID)
	fmt.Printf("Start ID: %d\n", config.StartID)
	fmt.Printf("End ID: %d\n", config.EndID)
	fmt.Printf("Output Directory: %s\n", config.OutputDir)
	fmt.Printf("Formats: %s\n", strings.Join(config.Formats, ","))
	fmt.Printf("Rate Limit Delay: %.1f seconds\n", config.RateLimitDelay)
	fmt.Printf("Max Consecutive Failures: %d\n", config.MaxConsecutiveFailures)
	fmt.Printf("Max Retries: %d\n", config.MaxRetries)
	fmt.Printf("Retry Delay: %.1f seconds\n", config.RetryDelay)
	fmt.Printf("Concurrency: %d\n", config.Concurrency)
	fmt.Printf("Resume: %v\n", config.Resume)
	fmt.Printf("Verbose: %v\n", config.Verbose)
	fmt.Println("")

	proceedStr := getInput("Proceed? (y/n)", "y")
	if strings.ToLower(proceedStr) != "y" && strings.ToLower(proceedStr) != "yes" {
		return nil, fmt.Errorf("export cancelled")
	}

	return config, nil
}

func main() {
	xTicketsHeader := flag.String("x-tickets-header", "", "Value for the x-tickets header (required)")
	authHeader := flag.String("auth-header", "", "Value for the authorization header (required)")
	serverID := flag.String("server-id", "", "Discord server ID (required)")
	startID := flag.Int("start-id", 1, "Starting transcript ID")
	endID := flag.Int("end-id", 2500, "Ending transcript ID")
	outputDir := flag.String("output-dir", "transcripts", "Directory to save transcripts")
	formats := flag.String("formats", "JSON,TXT,HTML", "Comma-separated list of output formats: JSON, TXT, HTML, CSV, MD")
	rateLimitDelay := flag.Float64("rate-limit-delay", 1.0, "Delay between requests in seconds")
	maxConsecutiveFailures := flag.Int("max-failures", 10, "Maximum consecutive failures before stopping")
	maxRetries := flag.Int("retries", 3, "Maximum retries per transcript")
	retryDelay := flag.Float64("retry-delay", 2.0, "Delay between retries in seconds")
	concurrency := flag.Int("concurrency", 1, "Number of concurrent requests")
	resume := flag.Bool("resume", false, "Resume from previous export")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	help := flag.Bool("help", false, "Display help message")
	noWait := flag.Bool("no-wait", false, "Don't wait for user input at the end of execution")

	flag.Parse()

	if *help {
		showHelp()
		if !*noWait {
			waitForEnter("Press Enter to exit...")
		}
		return
	}

	var config *Config
	var err error

	if *xTicketsHeader == "" || *authHeader == "" || *serverID == "" {
		config, err = runInteractiveMode()
		if err != nil {
			exitWithError(err.Error())
		}
	} else {
		formatsList := strings.Split(*formats, ",")
		for i, format := range formatsList {
			formatsList[i] = strings.TrimSpace(format)
		}

		if err := validateFormats(formatsList); err != nil {
			exitWithError(err.Error())
		}

		config = &Config{
			XTicketsHeader:         *xTicketsHeader,
			AuthHeader:             *authHeader,
			ServerID:               *serverID,
			StartID:                *startID,
			EndID:                  *endID,
			OutputDir:              *outputDir,
			Formats:                formatsList,
			RateLimitDelay:         *rateLimitDelay,
			MaxConsecutiveFailures: *maxConsecutiveFailures,
			MaxRetries:             *maxRetries,
			RetryDelay:             *retryDelay,
			Concurrency:            *concurrency,
			Resume:                 *resume,
			Verbose:                *verbose,
		}
	}

	logger, err := NewLogger(config.OutputDir, config.Verbose)
	if err != nil {
		exitWithError(fmt.Sprintf("Error creating logger: %s", err))
	}
	defer func(logger *Logger) {
		err := logger.Close()
		if err != nil {
			logger.Error("Error closing logger: %s", err)
		}
	}(logger)

	exporter := NewTranscriptExporter(*config, logger)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt signal. Saving progress and exiting...")
		exporter.SaveProgress()
		if !*noWait {
			waitForEnter("Press Enter to exit...")
		}
		os.Exit(0)
	}()

	fmt.Printf("Starting export from ID %d to %d...\n", config.StartID, config.EndID)
	startTime := time.Now()

	successful, failed := exporter.Run()

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	fmt.Println("\nExport completed!")
	fmt.Printf("Successfully exported: %d transcripts\n", successful)
	fmt.Printf("Failed to export: %d transcripts\n", failed)
	fmt.Printf("Total time: %.2f seconds (%.2f minutes)\n", duration.Seconds(), duration.Minutes())
	fmt.Printf("Transcripts saved to: %s\n", config.OutputDir)

	if !*noWait {
		waitForEnter("\nPress Enter to exit...")
	}
}

func showHelp() {
	fmt.Println("TicketsBot Transcript Exporter")
	fmt.Println("------------------------------------")
	fmt.Println("This program helps you export transcripts from the TicketsBot API.")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("")
	fmt.Println("Interactive mode (no required arguments): ./transcript-exporter")
	fmt.Println("Direct mode: ./transcript-exporter --x-tickets-header VALUE --auth-header VALUE --server-id VALUE [other options]")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  --x-tickets-header VALUE    Value for the x-tickets header (required)")
	fmt.Println("  --auth-header VALUE         Value for the authorization header (required)")
	fmt.Println("  --server-id VALUE           Discord server ID (required)")
	fmt.Println("  --start-id NUMBER           Starting transcript ID (default: 1)")
	fmt.Println("  --end-id NUMBER             Ending transcript ID (default: 2500)")
	fmt.Println("  --output-dir DIR            Directory to save transcripts (default: 'transcripts')")
	fmt.Println("  --formats LIST              Comma-separated list of output formats (default: JSON,TXT,HTML)")
	fmt.Println("                              Supported formats: JSON, TXT, HTML, CSV, MD")
	fmt.Println("  --rate-limit-delay NUMBER   Delay between requests in seconds (default: 1.0)")
	fmt.Println("  --max-failures NUMBER       Maximum consecutive failures before stopping (default: 10)")
	fmt.Println("  --retries NUMBER            Maximum retries per transcript (default: 3)")
	fmt.Println("  --retry-delay NUMBER        Delay between retries in seconds (default: 2.0)")
	fmt.Println("  --concurrency NUMBER        Number of concurrent requests (default: 1, sequential)")
	fmt.Println("  --resume                    Resume from previous export")
	fmt.Println("  --verbose                   Enable verbose logging")
	fmt.Println("  --no-wait                   Don't wait for user input at the end of execution")
	fmt.Println("  --help                      Display this help message")
	fmt.Println("")
}

func (e *TranscriptExporter) FetchTranscript(transID int) (*Transcript, error) {
	url := fmt.Sprintf("https://api.ticketsbot.cloud/api/%s/transcripts/%d/render", e.config.ServerID, transID)

	for attempt := 0; attempt < e.config.MaxRetries; attempt++ {
		e.logger.Debug("Requesting transcript %d, attempt %d/%d", transID, attempt+1, e.config.MaxRetries)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			e.logger.Error("Error creating request for transcript %d: %s", transID, err)
			return nil, err
		}

		req.Header.Set("x-tickets", e.config.XTicketsHeader)
		req.Header.Set("authorization", e.config.AuthHeader)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			e.logger.Error("Request error for transcript %d: %s", transID, err)
			if attempt < e.config.MaxRetries-1 {
				e.logger.Info("Retrying in %.1f seconds...", e.config.RetryDelay)
				time.Sleep(time.Duration(e.config.RetryDelay * float64(time.Second)))
				continue
			}
			return nil, err
		}

		defer func(Body io.ReadCloser) {
			err := Body.Close()
			if err != nil {
				e.logger.Error("Error closing response body: %s", err)
			}
		}(resp.Body)

		if resp.StatusCode == 429 {
			retryAfter := resp.Header.Get("Retry-After")
			waitTime := e.config.RetryDelay * 2
			if retryAfter != "" {
				if waitTimeParsed, err := strconv.ParseFloat(retryAfter, 64); err == nil {
					waitTime = waitTimeParsed
				}
			}
			e.logger.Warning("Rate limited. Waiting %.1f seconds...", waitTime)
			time.Sleep(time.Duration(waitTime * float64(time.Second)))
			continue
		}

		if resp.StatusCode == 200 {
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				e.logger.Error("Error reading response body for transcript %d: %s", transID, err)
				return nil, err
			}

			var transcript Transcript
			if err := json.Unmarshal(body, &transcript); err != nil {
				e.logger.Error("Error decoding JSON for transcript %d: %s", transID, err)
				if e.config.Verbose {
					e.logger.Error("Response content: %s...", string(body[:min(200, len(body))]))
				}
				return nil, err
			}

			if transcript.Error == "Transcript not found" || transcript.Success == false {
				e.logger.Info("Transcript %d not found", transID)
				return nil, nil
			}

			return &transcript, nil
		}

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			body, _ := io.ReadAll(resp.Body)
			var errorResp map[string]interface{}
			if err := json.Unmarshal(body, &errorResp); err == nil {
				if errorMsg, ok := errorResp["error"].(string); ok && errorMsg == "Missing x-tickets header" {
					e.logger.Critical("Missing or invalid x-tickets header. Please check your header value.")
					return nil, fmt.Errorf("missing or invalid x-tickets header")
				}
			}
		}

		e.logger.Warning("HTTP error %d for transcript %d", resp.StatusCode, transID)
		if e.config.Verbose {
			body, _ := io.ReadAll(resp.Body)
			e.logger.Warning("Response content: %s...", string(body[:min(200, len(body))]))
		}

		if attempt < e.config.MaxRetries-1 {
			e.logger.Info("Retrying in %.1f seconds...", e.config.RetryDelay)
			time.Sleep(time.Duration(e.config.RetryDelay * float64(time.Second)))
		} else {
			e.logger.Error("Failed to fetch transcript %d after %d attempts", transID, e.config.MaxRetries)
			return nil, fmt.Errorf("failed to fetch transcript after %d attempts", e.config.MaxRetries)
		}
	}

	return nil, fmt.Errorf("failed to fetch transcript after %d attempts", e.config.MaxRetries)
}

func (e *TranscriptExporter) SaveTranscript(transID int, data *Transcript) (bool, []string) {
	success := true
	var savedFormats []string

	for _, format := range e.config.Formats {
		formatDir := filepath.Join(e.config.OutputDir, strings.ToUpper(format))
		if err := os.MkdirAll(formatDir, 0755); err != nil {
			e.logger.Error("Error creating directory for format %s: %s", format, err)
			success = false
			continue
		}

		outputPath := filepath.Join(formatDir, fmt.Sprintf("transcript_%d.%s", transID, strings.ToLower(format)))

		var err error
		switch strings.ToUpper(format) {
		case "JSON":
			err = e.saveJSON(outputPath, data)
		case "TXT":
			err = e.saveTXT(outputPath, transID, data)
		case "HTML":
			err = e.saveHTML(outputPath, transID, data)
		case "CSV":
			err = e.saveCSV(outputPath, data)
		case "MD", "MARKDOWN":
			err = e.saveMarkdown(outputPath, transID, data)
		default:
			e.logger.Warning("Unsupported format: %s", format)
			continue
		}

		if err != nil {
			e.logger.Error("Error saving transcript %d in %s format: %s", transID, format, err)
			success = false
		} else {
			savedFormats = append(savedFormats, format)
		}
	}

	if len(savedFormats) > 0 {
		e.logger.Info("Saved transcript %d in formats: %s", transID, strings.Join(savedFormats, ", "))
	} else {
		success = false
	}

	return success, savedFormats
}

func (e *TranscriptExporter) saveJSON(outputPath string, data *Transcript) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			e.logger.Error("Error closing file: %s", err)
		}
	}(file)

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	return encoder.Encode(data)
}

func (e *TranscriptExporter) saveTXT(outputPath string, transID int, data *Transcript) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintf(file, "Transcript ID: %d\n\n", transID)
	fmt.Fprintf(file, "Channel: %s\n\n", data.ChannelName)

	users := make(map[string]User)
	if entities, ok := data.Entities["users"].(map[string]interface{}); ok {
		for userID, userData := range entities {
			if userMap, ok := userData.(map[string]interface{}); ok {
				username, _ := userMap["username"].(string)
				avatar, _ := userMap["avatar"].(string)
				users[userID] = User{
					Username: username,
					Avatar:   avatar,
				}
			}
		}
	}

	for _, msg := range data.Messages {
		author := "Unknown"
		if user, ok := users[msg.Author]; ok {
			author = user.Username
		}

		timestamp := time.Unix(0, msg.Time*int64(time.Millisecond))
		timeStr := timestamp.Format("2006-01-02 15:04:05")

		fmt.Fprintf(file, "[%s] %s: %s\n", timeStr, author, msg.Content)

		for _, embed := range msg.Embeds {
			if embed.Title != "" {
				fmt.Fprintf(file, "  Embed - %s\n", embed.Title)
			}
			if embed.Description != "" {
				fmt.Fprintf(file, "  %s\n", embed.Description)
			}

			for _, field := range embed.Fields {
				if field.Name != "" && field.Value != "" {
					fmt.Fprintf(file, "  %s: %s\n", field.Name, field.Value)
				}
			}
		}

		for _, attachment := range msg.Attachments {
			if attachment.Filename != "" {
				fmt.Fprintf(file, "  Attachment: %s", attachment.Filename)
				if attachment.URL != "" {
					fmt.Fprintf(file, " - %s", attachment.URL)
				}
				fmt.Fprintln(file)
			}
		}

		fmt.Fprintln(file)
	}

	return nil
}

func (e *TranscriptExporter) saveHTML(outputPath string, transID int, data *Transcript) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			e.logger.Error("Error closing file: %s", err)
		}
	}(file)

	fmt.Fprintf(file, `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Transcript %d</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .header { background-color: #f5f5f5; padding: 10px; border-radius: 5px; margin-bottom: 20px; }
        .message { margin-bottom: 15px; padding: 10px; border: 1px solid #e1e1e1; border-radius: 5px; }
        .author { font-weight: bold; }
        .time { color: #666; font-size: 0.8em; margin-left: 10px; }
        .content { margin-top: 10px; }
        .embed { margin-top: 10px; padding: 10px; background-color: #f9f9f9; border-left: 4px solid #ccc; }
        .embed-title { font-weight: bold; }
        .embed-description { margin-top: 5px; }
        .field { margin-top: 5px; }
        .field-name { font-weight: bold; }
        .attachment { margin-top: 5px; }
        .attachment a { color: #0066cc; text-decoration: none; }
        .attachment a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="header">
        <h1>Transcript %d</h1>
        <h2>Channel: %s</h2>
        <p>Exported on: %s</p>
    </div>
    <div class="messages">
`, transID, transID, data.ChannelName, time.Now().Format("2006-01-02 15:04:05"))

	users := make(map[string]User)
	if entities, ok := data.Entities["users"].(map[string]interface{}); ok {
		for userID, userData := range entities {
			if userMap, ok := userData.(map[string]interface{}); ok {
				username, _ := userMap["username"].(string)
				avatar, _ := userMap["avatar"].(string)
				users[userID] = User{
					Username: username,
					Avatar:   avatar,
				}
			}
		}
	}

	for _, msg := range data.Messages {
		author := "Unknown"
		if user, ok := users[msg.Author]; ok {
			author = user.Username
		}

		timestamp := time.Unix(0, msg.Time*int64(time.Millisecond))
		timeStr := timestamp.Format("2006-01-02 15:04:05")

		content := strings.Replace(msg.Content, "\n", "<br>", -1)

		fmt.Fprintf(file, `    <div class="message">
        <div class="author">%s<span class="time">%s</span></div>
        <div class="content">%s</div>
`, author, timeStr, content)

		for _, embed := range msg.Embeds {
			fmt.Fprintf(file, `        <div class="embed">
`)
			if embed.Title != "" {
				fmt.Fprintf(file, `            <div class="embed-title">%s</div>
`, embed.Title)
			}

			if embed.Description != "" {
				description := strings.Replace(embed.Description, "\n", "<br>", -1)
				fmt.Fprintf(file, `            <div class="embed-description">%s</div>
`, description)
			}

			for _, field := range embed.Fields {
				if field.Name != "" && field.Value != "" {
					value := strings.Replace(field.Value, "\n", "<br>", -1)
					fmt.Fprintf(file, `            <div class="field">
                <div class="field-name">%s:</div>
                <div class="field-value">%s</div>
            </div>
`, field.Name, value)
				}
			}

			fmt.Fprintf(file, `        </div>
`)
		}

		for _, attachment := range msg.Attachments {
			if attachment.Filename != "" && attachment.URL != "" {
				fmt.Fprintf(file, `        <div class="attachment">
            <a href="%s" target="_blank">%s</a>
        </div>
`, attachment.URL, attachment.Filename)
			}
		}

		fmt.Fprintf(file, "    </div>\n")
	}

	fmt.Fprintf(file, `
    </div>
</body>
</html>
`)

	return nil
}

func (e *TranscriptExporter) saveCSV(outputPath string, data *Transcript) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			e.logger.Error("Error closing file: %s", err)
		}
	}(file)

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{"Timestamp", "Author", "Content", "Embeds", "Attachments"}); err != nil {
		return err
	}

	users := make(map[string]User)
	if entities, ok := data.Entities["users"].(map[string]interface{}); ok {
		for userID, userData := range entities {
			if userMap, ok := userData.(map[string]interface{}); ok {
				username, _ := userMap["username"].(string)
				avatar, _ := userMap["avatar"].(string)
				users[userID] = User{
					Username: username,
					Avatar:   avatar,
				}
			}
		}
	}

	for _, msg := range data.Messages {
		author := "Unknown"
		if user, ok := users[msg.Author]; ok {
			author = user.Username
		}

		timestamp := time.Unix(0, msg.Time*int64(time.Millisecond))
		timeStr := timestamp.Format("2006-01-02 15:04:05")

		var embeds []string
		for _, embed := range msg.Embeds {
			if embed.Title != "" || embed.Description != "" {
				embeds = append(embeds, fmt.Sprintf("%s: %s", embed.Title, embed.Description))
			}
		}

		var attachments []string
		for _, attachment := range msg.Attachments {
			if attachment.Filename != "" {
				if attachment.URL != "" {
					attachments = append(attachments, fmt.Sprintf("%s (%s)", attachment.Filename, attachment.URL))
				} else {
					attachments = append(attachments, attachment.Filename)
				}
			}
		}

		if err := writer.Write([]string{
			timeStr,
			author,
			msg.Content,
			strings.Join(embeds, "; "),
			strings.Join(attachments, "; "),
		}); err != nil {
			return err
		}
	}

	return nil
}

func (e *TranscriptExporter) saveMarkdown(outputPath string, transID int, data *Transcript) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			e.logger.Error("Error closing file: %s", err)
		}
	}(file)

	fmt.Fprintf(file, "# Transcript %d\n\n", transID)
	fmt.Fprintf(file, "**Channel:** %s\n\n", data.ChannelName)
	fmt.Fprintf(file, "*Exported on: %s*\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(file, "---\n\n")

	users := make(map[string]User)
	if entities, ok := data.Entities["users"].(map[string]interface{}); ok {
		for userID, userData := range entities {
			if userMap, ok := userData.(map[string]interface{}); ok {
				username, _ := userMap["username"].(string)
				avatar, _ := userMap["avatar"].(string)
				users[userID] = User{
					Username: username,
					Avatar:   avatar,
				}
			}
		}
	}

	for _, msg := range data.Messages {
		author := "Unknown"
		if user, ok := users[msg.Author]; ok {
			author = user.Username
		}

		timestamp := time.Unix(0, msg.Time*int64(time.Millisecond))
		timeStr := timestamp.Format("2006-01-02 15:04:05")

		fmt.Fprintf(file, "### %s - %s\n\n", author, timeStr)
		if msg.Content != "" {
			fmt.Fprintf(file, "%s\n\n", msg.Content)
		}

		for _, embed := range msg.Embeds {
			if embed.Title != "" {
				fmt.Fprintf(file, "**%s**\n\n", embed.Title)
			}
			if embed.Description != "" {
				fmt.Fprintf(file, "%s\n\n", embed.Description)
			}

			for _, field := range embed.Fields {
				if field.Name != "" && field.Value != "" {
					fmt.Fprintf(file, "**%s:** %s\n\n", field.Name, field.Value)
				}
			}
		}

		for _, attachment := range msg.Attachments {
			if attachment.Filename != "" {
				if attachment.URL != "" {
					fmt.Fprintf(file, "[%s](%s)\n\n", attachment.Filename, attachment.URL)
				} else {
					fmt.Fprintf(file, "*Attachment: %s*\n\n", attachment.Filename)
				}
			}
		}

		fmt.Fprintf(file, "---\n\n")
	}

	return nil
}

func (e *TranscriptExporter) ProcessTranscript(transID int) bool {
	alreadyExists := true
	for _, format := range e.config.Formats {
		filePath := filepath.Join(e.config.OutputDir, strings.ToUpper(format), fmt.Sprintf("transcript_%d.%s", transID, strings.ToLower(format)))
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			alreadyExists = false
			break
		}
	}

	if alreadyExists {
		e.logger.Info("Transcript %d already exists in all formats. Skipping.", transID)
		return true
	}

	data, err := e.FetchTranscript(transID)
	if err != nil {
		return false
	}

	if data == nil {
		return false
	}

	success, _ := e.SaveTranscript(transID, data)

	time.Sleep(time.Duration(e.config.RateLimitDelay * float64(time.Second)))

	return success
}

func (e *TranscriptExporter) FindExportedTranscripts() map[int]bool {
	exportedIDs := make(map[int]bool)

	if len(e.config.Formats) == 0 {
		return exportedIDs
	}

	firstFormat := strings.ToUpper(e.config.Formats[0])
	formatDir := filepath.Join(e.config.OutputDir, firstFormat)

	entries, err := os.ReadDir(formatDir)
	if err != nil {
		return exportedIDs
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fileName := entry.Name()
		if !strings.HasPrefix(fileName, "transcript_") || !strings.HasSuffix(fileName, "."+strings.ToLower(firstFormat)) {
			continue
		}

		idStr := fileName[len("transcript_") : len(fileName)-len("."+strings.ToLower(firstFormat))]
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}

		exportedIDs[id] = true
	}

	return exportedIDs
}

func (e *TranscriptExporter) SaveProgress() {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	var completedIDs []int
	for id := range e.completedIDs {
		completedIDs = append(completedIDs, id)
	}

	state := State{
		StartID:      e.config.StartID,
		EndID:        e.config.EndID,
		CompletedIDs: completedIDs,
		Timestamp:    time.Now().Unix(),
	}

	stateFile := filepath.Join(e.config.OutputDir, "export_state.json")
	file, err := os.Create(stateFile)
	if err != nil {
		e.logger.Error("Error creating state file: %s", err)
		return
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			e.logger.Error("Error closing state file: %s", err)
		}
	}(file)

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		e.logger.Error("Error encoding state: %s", err)
	}
}

func (e *TranscriptExporter) LoadProgress() *State {
	stateFile := filepath.Join(e.config.OutputDir, "export_state.json")

	file, err := os.Open(stateFile)
	if err != nil {
		if !os.IsNotExist(err) {
			e.logger.Error("Error opening state file: %s", err)
		}
		return nil
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			e.logger.Error("Error closing state file: %s", err)
		}
	}(file)

	var state State
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&state); err != nil {
		e.logger.Error("Error decoding state: %s", err)
		return nil
	}

	return &state
}

func (e *TranscriptExporter) RunSequential() (int, int) {
	consecutiveFailures := 0
	successfulExports := 0
	failedExports := 0

	for _, format := range e.config.Formats {
		formatDir := filepath.Join(e.config.OutputDir, strings.ToUpper(format))
		if err := os.MkdirAll(formatDir, 0755); err != nil {
			e.logger.Error("Error creating directory for format %s: %s", format, err)
		}
	}

	e.completedIDs = e.FindExportedTranscripts()
	e.logger.Info("Found %d already exported transcripts", len(e.completedIDs))

	bar := pb.StartNew(e.config.EndID - e.config.StartID + 1)
	bar.SetTemplateString(`{{string . "prefix"}} {{counters . }} {{bar . }} {{percent . }} {{speed . }} {{string . "suffix"}}`)

	for transID := e.config.StartID; transID <= e.config.EndID; transID++ {
		bar.Set("prefix", fmt.Sprintf("Processing transcript %d", transID))

		if e.completedIDs[transID] {
			e.logger.Debug("Transcript %d already exported. Skipping.", transID)
			bar.Increment()
			successfulExports++
			continue
		}

		if consecutiveFailures >= e.config.MaxConsecutiveFailures {
			e.logger.Warning("Reached maximum consecutive failures (%d). Stopping.", e.config.MaxConsecutiveFailures)
			break
		}

		success := e.ProcessTranscript(transID)

		if success {
			consecutiveFailures = 0
			successfulExports++
			e.mutex.Lock()
			e.completedIDs[transID] = true
			e.mutex.Unlock()
		} else {
			consecutiveFailures++
			failedExports++
		}

		if (successfulExports+failedExports)%10 == 0 {
			e.SaveProgress()
		}

		bar.Increment()
	}

	bar.Finish()
	e.SaveProgress()

	return successfulExports, failedExports
}

func (e *TranscriptExporter) RunParallel() (int, int) {
	successfulExports := 0
	failedExports := 0

	for _, format := range e.config.Formats {
		formatDir := filepath.Join(e.config.OutputDir, strings.ToUpper(format))
		if err := os.MkdirAll(formatDir, 0755); err != nil {
			e.logger.Error("Error creating directory for format %s: %s", format, err)
		}
	}

	e.completedIDs = e.FindExportedTranscripts()
	e.logger.Info("Found %d already exported transcripts", len(e.completedIDs))

	var idsToProcess []int
	for transID := e.config.StartID; transID <= e.config.EndID; transID++ {
		if !e.completedIDs[transID] {
			idsToProcess = append(idsToProcess, transID)
		} else {
			successfulExports++
		}
	}

	e.logger.Info("Processing %d transcripts", len(idsToProcess))
	e.SaveProgress()

	bar := pb.StartNew(len(idsToProcess))
	bar.SetTemplateString(`{{string . "prefix"}} {{counters . }} {{bar . }} {{percent . }} {{speed . }} {{string . "suffix"}}`)

	consecutiveFailures := 0
	processedIDs := make(map[int]bool)

	batchSize := min(100, e.config.Concurrency*10)
	for batchStart := 0; batchStart < len(idsToProcess); batchStart += batchSize {
		batchEnd := min(batchStart+batchSize, len(idsToProcess))
		batchIDs := idsToProcess[batchStart:batchEnd]

		if consecutiveFailures >= e.config.MaxConsecutiveFailures {
			e.logger.Warning("Reached maximum consecutive failures (%d). Stopping.", e.config.MaxConsecutiveFailures)
			break
		}

		var wg sync.WaitGroup
		successChan := make(chan int, len(batchIDs))
		failureChan := make(chan int, len(batchIDs))

		idChan := make(chan int, len(batchIDs))
		for i := 0; i < e.config.Concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for transID := range idChan {
					bar.Set("prefix", fmt.Sprintf("Processing transcript %d", transID))
					success := e.ProcessTranscript(transID)
					if success {
						successChan <- transID
					} else {
						failureChan <- transID
					}
					bar.Increment()
				}
			}()
		}

		for _, transID := range batchIDs {
			idChan <- transID
		}
		close(idChan)

		wg.Wait()
		close(successChan)
		close(failureChan)

		for transID := range successChan {
			consecutiveFailures = 0
			successfulExports++
			e.mutex.Lock()
			e.completedIDs[transID] = true
			e.mutex.Unlock()
			processedIDs[transID] = true
		}

		for range failureChan {
			consecutiveFailures++
			failedExports++
		}

		if len(processedIDs)%10 == 0 {
			e.SaveProgress()
		}
	}

	bar.Finish()
	e.SaveProgress()

	return successfulExports, failedExports
}

func (e *TranscriptExporter) Run() (int, int) {
	if e.config.Resume {
		state := e.LoadProgress()
		if state != nil {
			e.config.StartID = state.StartID
			e.config.EndID = state.EndID
			e.logger.Info("Resuming export from previous state (start: %d, end: %d)", e.config.StartID, e.config.EndID)

			for _, id := range state.CompletedIDs {
				e.completedIDs[id] = true
			}
		}
	}

	if e.config.Concurrency > 1 {
		return e.RunParallel()
	}

	return e.RunSequential()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
