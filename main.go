package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	//"strconv"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// --- Constants ---
const (
	redisExpiry     = 24 * time.Hour
	crawlTimeout    = 30 * time.Second
	scrollAttempts  = 5
	pageLoadDelay   = 2 * time.Second
)

// --- Global Variables ---
var (
	db          *gorm.DB
	redisClient *redis.Client
)

// --- Regex Pattern for Product URLs ---
var productURLPattern = regexp.MustCompile(`/(dp|gp/product|product|item|shop|p)/[a-zA-Z0-9-_]+(/|\?|$)`)

// --- Database Model ---
type ProductURL struct {
	ID     uint   `gorm:"primaryKey"`
	Domain string `gorm:"index"`
	URL    string `gorm:"unique"`
}

// --- Crawl Result Struct ---
type CrawlResult struct {
	Domain string   `json:"domain"`
	URLs   []string `json:"urls"`
}

// --- Load Environment Variables ---
func loadEnv() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

// --- Initialize PostgreSQL Connection ---
func initDB() {
	loadEnv() // Load env variables

	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")
	dbPort := os.Getenv("DB_PORT")

	if dbHost == "" || dbUser == "" || dbPassword == "" || dbName == "" || dbPort == "" {
		log.Fatal("Database credentials are missing in .env file")
	}

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		dbHost, dbUser, dbPassword, dbName, dbPort)

	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}

	// Configure connection pooling
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to configure DB connection pool: %v", err)
	}
	sqlDB.SetMaxOpenConns(20) // Max 20 concurrent connections
	sqlDB.SetMaxIdleConns(10) // Keep 10 idle connections
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	// Auto-create table
	db.AutoMigrate(&ProductURL{})
	log.Println("Database initialized successfully")
}

// --- Initialize Redis Client ---
func initRedis() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR is missing in .env file")
	}

	redisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	_, err := redisClient.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Redis connected successfully")
}

// --- Check if URL is Already Visited (Redis) ---
func isURLVisited(url string) bool {
	exists, err := redisClient.Exists(context.Background(), url).Result()
	if err != nil {
		log.Printf("Redis error: %v", err)
		return false
	}
	return exists > 0
}

// --- Mark URL as Visited (Redis) ---
func markURLVisited(url string) {
	if err := redisClient.Set(context.Background(), url, 1, redisExpiry).Err(); err != nil {
		log.Printf("Failed to mark URL as visited: %v", err)
	}
}

// --- Handle Infinite Scrolling ---
func performInfiniteScroll(ctx context.Context) {
	for i := 0; i < scrollAttempts; i++ {
		err := chromedp.Run(ctx,
			chromedp.Evaluate(`window.scrollBy(0, document.body.scrollHeight)`, nil),
			chromedp.Sleep(time.Duration(rand.Intn(3)+2)*time.Second), // Random delay to mimic human behavior
		)
		if err != nil {
			log.Printf("Scrolling error: %v", err)
			return
		}
	}
}

// --- Handle Pagination ---
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
		chromedp.Sleep(pageLoadDelay),
	)
	return err == nil
}

// --- Store Product URLs in Database ---
func storeProductURLs(urls []string, domain string) {
	for _, url := range urls {
		// Ensure uniqueness before inserting into the database
		var count int64
		db.Model(&ProductURL{}).Where("url = ?", url).Count(&count)

		if count == 0 { // Insert only if URL doesn't exist
			db.Create(&ProductURL{Domain: domain, URL: url})
			log.Printf("Stored product URL: %s", url)
		} else {
			log.Printf("Duplicate URL skipped: %s", url)
		}
	}
}


// --- Extract Product URLs from Page ---
func extractProductURLs(htmlContent, baseURL string) []string {
	matches := productURLPattern.FindAllString(htmlContent, -1)
	uniqueURLs := make(map[string]bool)
	var productURLs []string

	for _, match := range matches {
		fullURL := baseURL + match
		if !uniqueURLs[fullURL] {
			uniqueURLs[fullURL] = true
			productURLs = append(productURLs, fullURL)
		}
	}
	return productURLs
}

// --- Scrape Product Pages ---
func scrapeWebsite(url string, resultChan chan<- CrawlResult, wg *sync.WaitGroup) {
	defer wg.Done()

	if isURLVisited(url) {
		log.Printf("Skipping already crawled URL: %s", url)
		return
	}
	markURLVisited(url)

	opts := chromedp.DefaultExecAllocatorOptions[:]
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, crawlTimeout)
	defer cancel()

	var htmlContent string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.OuterHTML(`html`, &htmlContent),
	)
	if err != nil {
		log.Printf("Failed to load page: %s | Error: %v", url, err)
		return
	}
	//
	log.Printf("Performing infinite scroll on: %s", url)
	performInfiniteScroll(ctx)
	chromedp.Run(ctx, chromedp.OuterHTML(`html`, &htmlContent))
	//

	productURLs := extractProductURLs(htmlContent, url)

	//
	storeProductURLs(productURLs, url)
	//

	for _, url := range productURLs {
		db.Create(&ProductURL{Domain: url, URL: url})
	}

	resultChan <- CrawlResult{Domain: url, URLs: productURLs}
}

// --- Save Results to JSON File ---
func saveResults(results []CrawlResult) {
	file, err := os.Create("output.json")
	if err != nil {
		log.Fatalf("Failed to create output file: %v", err)
	}
	defer file.Close()

	jsonData, _ := json.MarshalIndent(results, "", "  ")
	file.Write(jsonData)
	log.Println("Crawling complete. Results saved in output.json")
}

// --- Main Function ---
func main() {
	initDB()
	initRedis()

	domains := []string{
		"https://www.amazon.com/s?k=iphone",
		"https://www.snapdeal.com/search?keyword=mobile",
		"https://www.myntra.com/mobiles",
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
