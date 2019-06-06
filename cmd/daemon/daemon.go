package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

// ScrapeConfig defines the configuration for a scrape operation
type ScrapeConfig struct {
	Tag          string `json:"tag"`
	MonitorURL   string `json:"monitor_url"`
	MonitorLinks string `json:"monitor_links"`
	Title        string `json:"title"`
	Filter       string `json:"filter"`
	Email        string `json:"email"`
	Delay        int    `json:"delay"`
	SMTPServer   string `json:"smtp_server"`
}

// CachedLink is a watched link on the page
type CachedLink struct {
	Title string
	URL   string
}

// UA controls which user agent to use
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_14_1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/72.0.3626.119 Safari/537.36"

func get(url string) (*goquery.Document, error) {
	client := &http.Client{}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Error while making request %s: %s", url, err)
		return nil, err
	}

	req.Header.Set("User-Agent", UA)
	res, err := client.Do(req)
	if err != nil {
		log.Printf("Error while requesting %s: %s", url, err)
		return nil, err
	} else if res.StatusCode != 200 {
		log.Printf("Error while requesting %s: server responded %d", url, res.StatusCode)
		return nil, fmt.Errorf("server responded %d", res.StatusCode)
	}

	contentType := res.Header.Get("Content-Type")
	reader, err := charset.NewReader(res.Body, contentType)
	if err != nil {
		log.Printf("Error while converting encoding %s: %s", url, err)
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		log.Printf("Error while parsing document from %s: %s", url, err)
		return nil, err
	}

	return doc, nil
}

func check(conf *ScrapeConfig, prev []*CachedLink) ([]*CachedLink, []*CachedLink) {
	doc, err := get(conf.MonitorURL)
	if err != nil {
		log.Printf("Error while checking %s: %s", conf.MonitorURL, err)
		return prev, nil
	}

	var curr, news []*CachedLink

	results := doc.Find(conf.MonitorLinks)
	if results.Length() == 0 {
		log.Printf("Warning: no entry returned, skipping")
		return prev, nil
	}

	results.Each(func(i int, s *goquery.Selection) {
		url, exists := s.Attr("href")
		if !exists {
			log.Printf("Error: matched a non-link element")
		} else {
			var title string
			if conf.Title == "" {
				title = s.Text()
			} else {
				titleElements := s.Find(conf.Title)
				if titleElements.Length() == 0 {
					log.Printf("Warning: no title found for %s", url)
					title = "Untitled"
				} else {
					title = titleElements.Text()
				}
			}

			entry := &CachedLink{Title: title, URL: url}
			curr = append(curr, entry)
			for _, prevEntry := range prev {
				if entry.URL == prevEntry.URL {
					return
				}
			}
			news = append(news, entry)
		}
	})
	return curr, news
}

func sendPage(link *CachedLink, conf *ScrapeConfig) {
	doc, err := get(link.URL)
	if err != nil {
		log.Printf("Error while retrieving %s for sending: %s", conf.MonitorURL, err)
		return
	}

	content := ""
	results := doc.Find(conf.Filter)
	if results.Length() == 0 {
		log.Printf("Warning: no element returned from the filter for url %s", conf.MonitorURL)
		html, err := doc.Html()
		if err != nil {
			log.Printf("Error: failed to extract html from the document: %s", err)
		} else {
			content = html
		}
	} else {
		partials := []string{}
		results.Each(func(i int, s *goquery.Selection) {
			html, err := s.Html()
			if err != nil {
				log.Printf("Error: failed to extract html from the document: %s", err)
			} else {
				partials = append(partials, html)
			}
		})
		content = strings.Join(partials, "<hr>")
	}

	msg := fmt.Sprintf("From: gw2e\nSubject: %s\nTo: %s\nMIME-version: 1.0;\nContent-Type: text/html; charset=utf-8\n\n%s<hr>%s",
		link.Title, conf.Email, link.URL, content)
	err = smtp.SendMail(conf.SMTPServer, nil, "go_web_page_to_email", []string{conf.Email}, []byte(msg))
	if err != nil {
		log.Printf("Error: failed to send email: %s", err)
	}
}

func main() {
	confPath := flag.String("conf", "config.json", "path to the configuration")
	flag.Parse()

	log.Printf("loading configurations...")
	confFile, err := ioutil.ReadFile(*confPath)
	if err != nil {
		panic(err)
	}

	var conf ScrapeConfig
	err = json.Unmarshal(confFile, &conf)
	if err != nil {
		panic(err)
	}

	var (
		cache, news []*CachedLink
		notfirst    bool
	)

	for {
		// log.Printf("checking %s...", conf.Tag)
		base, err := url.Parse(conf.MonitorURL)
		if err != nil {
			log.Printf("Error URL expected for \"%s\": %s", conf.MonitorURL, err)
			continue
		}
		cache, news = check(&conf, cache)
		if len(news) != 0 {
			if !notfirst {
				log.Printf("cached %d entries.", len(news))
				notfirst = true
				for _, n := range news {
					log.Printf("%s\t%s", n.Title, n.URL)
				}
			} else {
				log.Printf("found %d new entries.", len(news))
				for _, n := range news {
					u, err := url.Parse(n.URL)
					if err != nil {
						log.Printf("Error: url expected for \"%s\": %s", n, err)
					} else {
						link := CachedLink{
							Title: conf.Tag + " | " + n.Title,
							URL:   base.ResolveReference(u).String(),
						}
						sendPage(&link, &conf)
					}
				}
			}
		}
		// log.Printf("waiting for next check...")
		time.Sleep(time.Duration(conf.Delay) * time.Second)
	}
}
