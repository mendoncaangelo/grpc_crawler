## Multiple Site crawler
  The application consists of implementing a "Web Crawler as a gRPC service". It consists of a command line client and a local service which runs the actual web crawling. The communication between client and server is defined as a gRPC service. The crawler only follows links on the
  domain of the provided URL and not any external links. It uses channels and goroutines for enhanced performance.

## Installation

  Standard `go get`:

  ```
  $ go get github.com/mendoncaangelo/grpc_crawler
  ```

  Make sure you have go installed and the GOPATH set. 
  
  You need to get "github.com/codegangsta/cli"
  go get -u github.com/codegangsta/cli

  git clone git@github.com/mendoncaangelo/grpc_crawler.git $GOPATH/src/


## Usage 
  ```
  go run server.go -- will start the local server
  ```

  ```
  go run client.go start url_name -- will start crawling
  ```

  ```
  e.g. go run client.go start www.hashicorp.com
  ```

  ```
  go run client.go list -- will list the urls crawled so far. 
  ```

  To stop a specific site crawler
  ```
  e.g. go run client.go stop www.hashicorp.com
  ```

  You can also start other crawlers for different websites while others sites are being crawled.
  ```
  e.g. go run client.go start www.nodejs.org
  ```

  You can stop one of the crawlers and have the other one running. 
  ```
  e.g. go run client.go stop www.hashicorp.com
  ```

## Enhancements
  Some of the parsing of links needs to be improved. Already visited URLs need to be ignored. 
