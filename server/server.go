package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	pb "github.com/mendoncaangelo/grpc_crawler/crawler"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// server is used to implement the crawler.CrawlerServer.
type server struct {
	spiderPtr *crawlerDS
}

// Crawler Interface...
type Crawler interface {
	pushURLToWaitingList(string, int, *chan int)
	parseLinks(*string, string, int, *chan int)
	crawler(string, int, *chan int)

	parseURL(string) (bool, string)
	listURLS() []string
	startCrawling()
	initHTTPRequestCapacity()
}

type linkIndex struct {
	index int
	url   string
}

const (
	port               = ":40052"
	concurrentRequests = 50
)

type crawlerDS struct {
	// Complete list of URLS visited and if they were put on channel to be visited.
	visitedUrls, urlOnChannel map[string]bool

	// URLS that were crawled
	finishedUrls chan string
	// Urls currently waiting to be crawled
	waitingUrls chan linkIndex

	// URL to Index Mapper
	siteURLIndex map[string]*linkIndex

	// Index to URL Mapper
	siteIndexURL map[int]string
	// Crawler Specific channel terminator
	terminate []*chan int
	// Number of sites being crawled
	siteIndex int
}

var (
	wg                          sync.WaitGroup
	mapLock                     = sync.RWMutex{}
	newsites                    = make(chan string, 1)
	shutdownSpecificSiteCrawler = make(chan linkIndex, 1)
	httpLimitChannel            = make(chan int, concurrentRequests)
)

func (c *crawlerDS) initHTTPRequestCapacity() {
	for i := 0; i < concurrentRequests; i++ {
		httpLimitChannel <- 1
	}
	return
}

// Method to find if uri has scheme and extract the hostname
func (c *crawlerDS) parseURL(uri string) (bool, string) {
	pg, err := url.Parse(uri)
	if err != nil {
		log.Fatal(err)
	}
	if pg.Scheme == "https" || pg.Scheme == "http" {
		return true, pg.Host
	}
	return false, ""

}

// pushes the new urls to the waiting urls channel to be processed
func (c *crawlerDS) pushURLToWaitingList(url string, index int, shutdown *chan int) {

	select {
	case _ = <-*shutdown:
		fmt.Println("Shutting down", url, index)
		return
	default:
		c.waitingUrls <- linkIndex{index: index, url: url}
	}
}

// Method that processes the href's in the crawled page
func (c *crawlerDS) parseLinks(data *string, pageURL string, index int, shutdown *chan int) {

	defer wg.Done()
	u, err := url.Parse(pageURL)
	if err != nil {
		log.Fatal(err)
	}
	re := regexp.MustCompile("href=\"(.*?)\"")
	subre := regexp.MustCompile("\"/[\\w]+")

	matchLink := re.FindAllStringSubmatch(string(*data), -1)
	for _, lk := range matchLink {

		if subre.MatchString(lk[0]) && lk[1] != pageURL {
			url := pageURL + lk[1]
			scheme, _ := c.parseURL(url)

			if scheme {
				c.pushURLToWaitingList(url, index, shutdown)
			}

		} else if strings.Contains(lk[1], u.Hostname()) {
			scheme, host := c.parseURL(lk[1])
			init, _ := url.Parse(pageURL)

			if scheme && host != init.Host {
				c.pushURLToWaitingList(lk[1], index, shutdown)
			}
		}
	}
}

// Method to crawl
func (c *crawlerDS) crawler(url string, index int, shutdown *chan int) {

	defer wg.Done()
	// This will limit the number of HTTP requests.
	<-httpLimitChannel
	resp, err := http.Get(url)
	httpLimitChannel <- 1

	if err != nil {
		log.Fatal(err)
	} else {
		select {
		case _ = <-*shutdown:
			return
		default:
			// push the crawled url onto the finishedURL channel
			c.finishedUrls <- url
			defer resp.Body.Close()
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				log.Fatal(err)
			}
			msg := string(body)
			wg.Add(1)
			go c.parseLinks(&msg, url, index, shutdown)
		}

	}
}

// Method to print the site tree.
func (c *crawlerDS) listURLS() []string {
	mapLock.RLock()
	defer mapLock.RUnlock()
	links := make([]string, 0)
	for k := range c.visitedUrls {
		links = append(links, k)
	}

	return links
}

func (c *crawlerDS) startCrawling() {
	defer wg.Done()
	wg.Add(1)
	// Initialize the request throttler channel
	go c.initHTTPRequestCapacity()

	for {
		select {

		case finURL, ok := <-c.finishedUrls:
			if ok {
				fmt.Println("Crawled URL", finURL)
				mapLock.Lock()
				c.visitedUrls[finURL] = true
				mapLock.Unlock()
			}
		case obj := <-c.waitingUrls:
			wg.Add(1)
			go c.crawler(obj.url, obj.index, c.terminate[obj.index])

		case url := <-newsites:
			fmt.Println("Received new site to crawl", len(newsites), url, c.siteIndex)

			// Every site should have a shutdown channel
			shutdown := make(chan int, 1)
			mapLock.Lock()
			c.terminate = append(c.terminate, &shutdown)

			c.siteURLIndex[url] = &linkIndex{index: c.siteIndex, url: url}
			c.siteIndexURL[c.siteIndex] = url
			mapLock.Unlock()

			wg.Add(1)
			go c.crawler(c.siteIndexURL[c.siteIndex], c.siteIndex, &shutdown)
			c.siteIndex++

		case obj := <-shutdownSpecificSiteCrawler:
			mapLock.RLock()
			close(*c.terminate[obj.index])
			delete(c.siteURLIndex, obj.url)
			delete(c.siteIndexURL, obj.index)
			mapLock.RUnlock()

		default:
			time.Sleep(5 * time.Second)
			continue
		}

	}
	wg.Wait()

}

// Crawl method implementation for the gRPC
func (s *server) Crawl(ctx context.Context, in *pb.LinkRequest) (*pb.CrawlerResponse, error) {
	mapLock.RLock()
	// given a URL checks to see if its currently being crawled
	_, exists := s.spiderPtr.siteURLIndex[in.Url]
	mapLock.RUnlock()
	if exists {
		msg := fmt.Sprintf("Site %s is already being crawled", in.Url)
		return &pb.CrawlerResponse{Message: msg}, nil
	}
	// put new site on channel
	newsites <- in.Url
	return &pb.CrawlerResponse{Message: "Crawler started crawling"}, nil
}

// Stop method implementation for the gRPC
func (s *server) Stop(ctx context.Context, in *pb.StopRequest) (*pb.StopResponse, error) {
	mapLock.RLock()
	// given a URL checks to see if its currently not being crawled
	indx, exists := s.spiderPtr.siteURLIndex[in.Link]
	mapLock.RUnlock()
	if !exists {
		msg := fmt.Sprintf("Site %s is not being crawled", in.Link)
		return &pb.StopResponse{Message: msg}, nil
	}
	shutdownSpecificSiteCrawler <- linkIndex{url: in.Link, index: indx.index}

	return &pb.StopResponse{Message: "Crawler Stopping"}, nil
}

// List Visited URLS implementation for the gRPC
func (s *server) ListVisitedUrls(in *pb.ListRequest, stream pb.Crawler_ListVisitedUrlsServer) error {

	for _, link := range s.spiderPtr.listURLS() {
		ln := &pb.LinkRequest{Url: link}
		if err := stream.Send(ln); err != nil {
			return err
		}
	}
	return nil
}

// gRPC main server process
func main() {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}

	// Creates a new gRPC server
	srvr := grpc.NewServer()
	ds := crawlerDS{visitedUrls: make(map[string]bool),
		urlOnChannel: make(map[string]bool),
		siteURLIndex: make(map[string]*linkIndex),
		siteIndex:    0,
		finishedUrls: make(chan string),
		siteIndexURL: make(map[int]string),
		waitingUrls:  make(chan linkIndex),
		terminate:    make([]*chan int, 0)}

	wg.Add(1)
	go ds.startCrawling()

	pb.RegisterCrawlerServer(srvr, &server{spiderPtr: &ds})
	srvr.Serve(lis)
}
