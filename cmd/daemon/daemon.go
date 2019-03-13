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
}

// CachedLink is a watched link on the page
type CachedLink struct {
	Title string
	URL   string
}

// UA controls which user agent to use
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_14_1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/72.0.3626.119 Safari/537.36"

func get(url string) (*http.Response, error) {
	client := &http.Client{}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("ERROR request %s: %s", url, err)
		return nil, err
	}

	req.Header.Set("User-Agent", UA)
	return client.Do(req)
}

func check(conf *ScrapeConfig, prev []*CachedLink) ([]*CachedLink, []*CachedLink) {
	res, err := get(conf.MonitorURL)
	if err != nil {
		log.Printf("ERROR cannot read %s: %s", conf.MonitorURL, err)
		return prev, nil
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		log.Printf("ERROR cannot read %s: server responded %d", conf.MonitorURL, res.StatusCode)
		return prev, nil
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Printf("ERROR cannot parse server response: %s", err)
		return prev, nil
	}

	var curr, news []*CachedLink

	results := doc.Find(conf.MonitorLinks)
	if results.Length() == 0 {
		log.Printf("WARN no entry returned, skipping")
		return prev, nil
	}

	results.Each(func(i int, s *goquery.Selection) {
		url, exists := s.Attr("href")
		if !exists {
			log.Printf("ERROR matched a non-link element")
		} else {
			var title string
			if conf.Title == "" {
				title = s.Text()
			} else {
				titleElements := s.Find(conf.Title)
				if titleElements.Length() == 0 {
					log.Printf("WARN no title found for %s", url)
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

func sendPage(email, title, url, filter string) {
	res, err := get(url)
	if err != nil {
		log.Printf("ERROR cannot read %s: %s", url, err)
		return
	}
	defer res.Body.Close()

	var content string
	if res.StatusCode == 200 {
		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			log.Printf("ERROR cannot parse server response: %s", err)
			return
		}

		results := doc.Find(filter)
		if results.Length() == 0 {
			log.Printf("ERROR no content retrieved")
			content = fmt.Sprintf("<a href=\"%s\">%s</a>", url, url)
		} else {
			parts := []string{url}
			results.Each(func(i int, s *goquery.Selection) {
				html, err := s.Html()
				if err != nil {
					log.Printf("ERROR failed to extract html: %s", err)
				} else {
					parts = append(parts, html)
				}
			})
			content = strings.Join(parts, "<hr>")
		}
	} else if res.StatusCode == 302 {
		log.Printf("INFO link is a redirect")
		content = fmt.Sprintf("<a href=\"%s\">%s</a>", url, url)
	} else {
		log.Printf("ERROR cannot read %s: server responded %d", url, res.StatusCode)
		return
	}

	msg := fmt.Sprintf("Subject %s\nTo: %s\nMIME-version: 1.0;\nContent-Type: text/html;\n\n%s",
		title, email, content)
	err = smtp.SendMail("127.0.0.1:25", nil, "root@localhost", []string{email}, []byte(msg))
	if err != nil {
		log.Printf("ERROR send mail failed: %s", err)
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

		log.Printf("checking %s...", conf.Tag)
		base, err := url.Parse(conf.MonitorURL)
		if err != nil {
			log.Printf("ERROR URL expected for \"%s\": %s", conf.MonitorURL, err)
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
						log.Printf("ERROR URL expected for \"%s\": %s", n, err)
					} else {
						sendPage(conf.Email, conf.Tag+" | "+n.Title, base.ResolveReference(u).String(), conf.Filter)
					}
				}
			}
		} else {
			log.Printf("no new entry found.")
		}
		log.Printf("waiting for next check...")
		time.Sleep(time.Duration(conf.Delay) * time.Second)
	}
}
