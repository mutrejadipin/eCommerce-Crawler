package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Redis client for duplicate URL handling
var redisClient = redis.NewClient(&redis.Options{Addr: "localhost:6379"})

// PostgreSQL database instance
var db *gorm.DB

// Proxy server for avoiding IP bans
var proxyServer = "http://your-proxy-server:port"

// Product URL regex pattern
var ProductURLRegex = regexp.MustCompile(`/(dp|gp/product|product|item|shop|p)/[a-zA-Z0-9-_]+(/|\?|$)`)

// Struct to store product URLs in PostgreSQL
type ProductURL struct {
	ID     uint   `gorm:"primaryKey"`
	Domain string `gorm:"index"`
	URL    string `gorm:"unique"`
}

// Struct for JSON output
type CrawlResult struct {
	Domain string   `json:"domain"`
	URLs   []string `json:"urls"`
}

// Initialize PostgreSQL database
func initDB() {
	var err error
	dsn := "host=localhost user=postgres password=yourpassword dbname=crawler sslmode=disable"
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to DB:", err)
	}
	db.AutoMigrate(&ProductURL{}) // Auto-create table
}

// Check if a URL is already visited (Redis)
func isURLVisited(url string) bool {
	exists, _ := redisClient.Exists(context.Background(), url).Result()
	return exists > 0
}

// Mark a URL as visited in Redis
func markURLVisited(url string) {
	redisClient.Set(context.Background(), url, 1, 24*time.Hour) // Store for 24 hours
}

// Function to handle infinite scrolling
func infiniteScroll(ctx context.Context) {
	for i := 0; i < 5; i++ {
		err := chromedp.Run(ctx,
			chromedp.Evaluate(`window.scrollBy(0, document.body.scrollHeight)`, nil),
			chromedp.Sleep(time.Duration(rand.Intn(3)+2)*time.Second),
		)
		if err != nil {
			log.Println("Scrolling error:", err)
			return
		}
	}
}

// Function to handle pagination
func clickNextPage(ctx context.Context) bool {
	var nextExists bool
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('a.next-page') !== null`, &nextExists),
	)
	if err != nil || !nextExists {
		return false
	}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('a.next-page').click()`, nil),
		chromedp.Sleep(time.Duration(rand.Intn(3)+2)*time.Second),
	)
	return err == nil
}

// Function to extract category links
func extractCategoryLinks(ctx context.Context) []string {
	var categoryLinks []string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`Array.from(document.querySelectorAll('a.category-link')).map(a => a.href)`, &categoryLinks),
	)
	if err != nil {
		log.Println("Category extraction failed:", err)
		return nil
	}
	return categoryLinks
}

// Function to crawl product pages
func scrapeWebsite(url string, resultChan chan<- CrawlResult, wg *sync.WaitGroup) {
	defer wg.Done()

	if isURLVisited(url) {
		fmt.Println("Skipping already crawled URL:", url)
		return
	}
	markURLVisited(url)

	// Set up Chrome with proxy for avoiding bans
	// opts := append(chromedp.DefaultExecAllocatorOptions[:],
	// 	chromedp.ProxyServer(proxyServer),
	// )
	opts := chromedp.DefaultExecAllocatorOptions[:]
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Increase timeout for loading large pages
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var htmlContent string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			infiniteScroll(ctx) // Handle lazy-loaded content
			return nil
		}),
		chromedp.OuterHTML(`html`, &htmlContent),
	)
	if err != nil {
		log.Println("Failed to load page:", url, err)
		return
	}

	// Extract category links
	categoryLinks := extractCategoryLinks(ctx)

	// Extract product URLs
	matches := ProductURLRegex.FindAllString(htmlContent, -1)
	uniqueURLs := make(map[string]bool)
	var productURLs []string

	for _, match := range matches {
		fullURL := url + match
		if !uniqueURLs[fullURL] {
			uniqueURLs[fullURL] = true
			productURLs = append(productURLs, fullURL)
			db.Create(&ProductURL{Domain: url, URL: fullURL}) // Store in DB
		}
	}

	// Handle pagination
	for clickNextPage(ctx) {
		chromedp.Run(ctx, chromedp.OuterHTML(`html`, &htmlContent))
		matches := ProductURLRegex.FindAllString(htmlContent, -1)
		for _, match := range matches {
			fullURL := url + match
			if !uniqueURLs[fullURL] {
				uniqueURLs[fullURL] = true
				productURLs = append(productURLs, fullURL)
				db.Create(&ProductURL{Domain: url, URL: fullURL})
			}
		}
	}

	// Recursively crawl category pages
	for _, catURL := range categoryLinks {
		wg.Add(1)
		go scrapeWebsite(catURL, resultChan, wg)
	}

	resultChan <- CrawlResult{Domain: url, URLs: productURLs}
}

// Save results to JSON file
func saveResults(results []CrawlResult) {
	file, err := os.Create("output.json")
	if err != nil {
		log.Fatal("Failed to create output file:", err)
	}
	defer file.Close()

	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Fatal("Failed to marshal JSON:", err)
	}

	file.Write(jsonData)
	fmt.Println("Crawling complete. Results saved in output.json")
}

// Main function
func main() {
	initDB()

	domains := []string{
		// "https://www.amazon.com/s?k=laptops",
		// "https://www.flipkart.com/search?q=laptops",
		// "https://www.ebay.com/sch/i.html?_nkw=laptops",
		//"https://www.snapdeal.com/search?keyword=laptops",
		"https://www.myntra.com/laptops",
	}

	var results []CrawlResult
	resultChan := make(chan CrawlResult, len(domains))
	var wg sync.WaitGroup

	for _, domain := range domains {
		wg.Add(1)
		go scrapeWebsite(domain, resultChan, &wg)
	}

	wg.Wait()
	close(resultChan)

	for res := range resultChan {
		results = append(results, res)
	}

	saveResults(results)
}
