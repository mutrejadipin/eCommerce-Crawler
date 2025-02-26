# Go Web Crawler for extracting product URLs on E-commerce websites


##  Overview
This project is a **scalable web crawler** designed to **discover and list product URLs** from multiple e-commerce websites.  
It handles **JavaScript-rendered pages, infinite scrolling, pagination, and URL variations**, while **storing data efficiently** in PostgreSQL and preventing duplicate crawling with Redis.

---

## Features
**Multi-Domain Crawling** → Handles 10+ e-commerce domains in parallel  
**Extracts Product URLs** → Identifies product pages using regex patterns  
**JavaScript Handling** → Uses **Chromedp** to process dynamic pages  
**Infinite Scrolling Support** → Scrolls down dynamically loaded product lists  
**Pagination Handling** → Clicks **"Next Page"** buttons to extract all products  
**Proxy Support** → Prevents IP bans using rotating proxies (optional)  
**Stores Data in PostgreSQL** → Saves structured product URLs in a database  
**Prevents Duplicate Crawling** → Uses **Redis** to avoid revisiting URLs  

---



## Installation & Setup
**Install dependencies**:
go mod tidy

**Run redis**:
docker run --name redis-server -d -p PORT:PORT redis

**Run postgres server inside docker**:
docker run --name postgres -e POSTGRES_USER="user_name" -e POSTGRES_PASSWORD="password" -e POSTGRES_DB="db_name" -d -p PORT:PORT postgres

**Run crawler**:
go run main.go

**check Redis data**:
docker exec -it redis redis-cli

**check postgres data**:
docker exec -it postgres psql -U user_name -d db_name

**check extracted data in DB**
SELECT * FROM product_urls LIMIT 10;


## Architecture & Approach
Crawling Process
Uses Colly to extract links from web pages.
If the page is JavaScript-rendered, it loads it in ChromeDP.
Filters URLs to only keep product pages using regular expressions.
Saves unique product URLs per domain to JSON.

High level architecture:
User → CLI → Crawler → Extract URLs → Filter Product Pages → Store Results



### **Input: List of E-commerce Domains**
The crawler accepts a **list of domains** where products need to be extracted.  
Example:
```go
domains := []string{
    "https://www.amazon.com/s?k=laptops",
    "https://www.flipkart.com/search?q=laptops",
    "https://www.ebay.com/sch/i.html?_nkw=laptops",
}

### **Output: **
 id |           domain           |               url                
----+----------------------------+---------------------------------
  1 | https://www.amazon.com     | https://www.amazon.com/dp/B09XYZ
  2 | https://www.flipkart.com   | https://www.flipkart.com/p/23456
  3 | https://www.ebay.com       | https://www.ebay.com/itm/445566

```