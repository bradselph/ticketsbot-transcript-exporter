# TicketsBot Transcript Exporter

A lightweight Go application to bulk export transcripts from the TicketsBot API. This tool allows you to download ticket transcripts in multiple formats from the TicketsBot dashboard.

## Features

- Export ticket transcripts in multiple formats (JSON, TXT, HTML, CSV, Markdown)
- Resume interrupted exports
- Concurrent download support
- Rate limit handling
- Detailed logging
- Progress tracking
- Single executable with no dependencies

## Requirements

- None! The application is compiled into a single executable with no external dependencies

## Installation

### Download pre-built binary

Download the latest release from the [releases page](https://github.com/bradselph/ticketsbot-transcript-exporter/releases).

### Build from source

1. Clone this repository:
```bash
git clone https://github.com/bradselph/ticketsbot-transcript-exporter.git
cd ticketsbot-transcript-exporter
```

2. Build the application:
```bash
go build -o transcript-exporter
```

## Usage

### Using Interactive Mode

Run the executable without any required arguments to use the interactive mode:

```bash
./transcript-exporter
```

The application will prompt you for all required information.

### Using Command Line Arguments

You can also run the exporter directly with command line arguments:

```bash
./transcript-exporter --x-tickets-header YOUR_XTICKETS_VALUE --auth-header YOUR_AUTH_VALUE --server-id YOUR_SERVER_ID
```

### Required Arguments

- `--x-tickets-header VALUE`: Value for the x-tickets header (required)
- `--auth-header VALUE`: Value for the authorization header (required)
- `--server-id VALUE`: Discord server ID (required)

### Optional Arguments

- `--start-id NUMBER`: Starting transcript ID (default: 1)
- `--end-id NUMBER`: Ending transcript ID (default: 2500)
- `--output-dir DIR`: Directory to save transcripts (default: 'transcripts')
- `--formats LIST`: Comma-separated list of output formats (default: JSON,TXT,HTML)
  - Supported formats: JSON, TXT, HTML, CSV, MD
- `--rate-limit-delay NUMBER`: Delay between requests in seconds (default: 1.0)
- `--max-failures NUMBER`: Maximum consecutive failures before stopping (default: 10)
- `--retries NUMBER`: Maximum retries per transcript (default: 3)
- `--retry-delay NUMBER`: Delay between retries in seconds (default: 2.0)
- `--concurrency NUMBER`: Number of concurrent requests (default: 1)
- `--resume`: Resume from previous export
- `--verbose`: Enable verbose logging
- `--no-wait`: Don't wait for user input at the end of execution (useful for automation)
- `--help`: Display help message

## Obtaining the Required Headers

To use this application, you need both the x-tickets and authorization header values from your browser:

1. Log in to your TicketsBot dashboard at https://dashboard.ticketsbot.cloud/
2. Open a transcript page
3. Open your browser's developer tools (F12 or right-click > Inspect)
4. Go to the Network tab
5. View a transcript and look for a request to "render"
6. In the request headers, find the "x-tickets" and "authorization" header values
7. Copy these values and use them with the application

## Finding Your Server ID

You also need your Discord server ID:

1. Open Discord and go to your server
2. Right-click on your server name and select "Copy ID" (you may need to enable Developer Mode in Discord settings)
3. Use this ID with the `--server-id` parameter

## Output Formats

- **JSON**: Raw transcript data in JSON format
- **TXT**: Simple text format with messages and timestamps
- **HTML**: Formatted HTML with styling for easy reading in a browser
- **CSV**: Comma-separated values format for spreadsheet applications
- **MD/MARKDOWN**: Markdown format for easy reading in text editors

## Examples

Export all transcripts from ID 1 to 1000 in JSON, TXT, and HTML formats:

```bash
./transcript-exporter --x-tickets-header "your-xtickets-value" --auth-header "your-auth-value" --server-id "your-server-id" --start-id 1 --end-id 1000
```

Export transcripts with 3 concurrent downloads:

```bash
./transcript-exporter --x-tickets-header "your-xtickets-value" --auth-header "your-auth-value" --server-id "your-server-id" --concurrency 3
```

Resume a previously interrupted export:

```bash
./transcript-exporter --x-tickets-header "your-xtickets-value" --auth-header "your-auth-value" --server-id "your-server-id" --resume
```

Run in non-interactive mode for automation:

```bash
./transcript-exporter --x-tickets-header "your-xtickets-value" --auth-header "your-auth-value" --server-id "your-server-id" --no-wait
```

## Troubleshooting

- If you get a "Missing x-tickets header" error, ensure your header values are correct and properly quoted
- If you encounter rate limiting, increase the `--rate-limit-delay` value
- For slow connections, you may need to increase `--retry-delay` and `--retries`
- Check the log file in the output directory for detailed error information

## License

This project is licensed under the GNU Affero General Public License v3.0 (AGPL-3.0) - see the LICENSE file for details.