package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/gorilla/websocket"
	"github.com/toqueteos/webbrowser"
)

// Configure the WebSocket upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all connections for this simple proxy
		return true
	},
}

//go:embed r3Toolshop.html
var embeddedFS embed.FS

func main() {
	http.HandleFunc("/", serveSPA)

	// handlers for proxying
	http.HandleFunc("/ws", handleWebSocketProxy)
	http.HandleFunc("/proxy-schema", handleSchemaProxy)

	port := "8080"
	fmt.Printf("Starting R3 Toolshop server on http://localhost:%s\n", port)
	webbrowser.Open("http://localhost:" + port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Check if an external r3Toolshop.html exists
	_, err := os.Stat("r3Toolshop.html")
	if err == nil {
		// If it exists, serve the external file.
		// This allows for easy development and customization without rebuilding.
		log.Println("Serving external r3Toolshop.html")
		http.ServeFile(w, r, "r3Toolshop.html")
		return
	}

	// If the external file doesn't exist, serve the embedded version.
	log.Println("Serving embedded r3Toolshop.html")
	file, err := embeddedFS.Open("r3Toolshop.html")
	if err != nil {
		log.Printf("FATAL: Could not open embedded r3Toolshop.html: %v", err)
		http.Error(w, "Internal Server Error: Embedded file not found.", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		log.Printf("FATAL: Could not stat embedded r3Toolshop.html: %v", err)
		http.Error(w, "Internal Server Error: Cannot stat embedded file.", http.StatusInternalServerError)
		return
	}

	// Read the file content into a buffer to create an io.ReadSeeker, which http.ServeContent needs.
	content, err := io.ReadAll(file)
	if err != nil {
		log.Printf("FATAL: Could not read embedded r3Toolshop.html: %v", err)
		http.Error(w, "Internal Server Error: Cannot read embedded file.", http.StatusInternalServerError)
		return
	}
	reader := bytes.NewReader(content)

	// Use http.ServeContent to handle caching headers correctly.
	http.ServeContent(w, r, "r3Toolshop.html", stat.ModTime(), reader)
}

// handleWebSocketProxy proxies WebSocket connections to the target R3 server.
func handleWebSocketProxy(w http.ResponseWriter, r *http.Request) {
	targetHost := r.URL.Query().Get("target")
	if targetHost == "" {
		http.Error(w, "Missing 'target' query parameter", http.StatusBadRequest)
		return
	}

	// The target URL for the R3 WebSocket server
	targetURL := url.URL{Scheme: "ws", Host: targetHost, Path: "/websocket"}
	log.Printf("Proxying WebSocket to: %s", targetURL.String())

	// Dial the target server
	serverConn, _, err := websocket.DefaultDialer.Dial(targetURL.String(), nil)
	if err != nil {
		log.Printf("Error dialing target WebSocket server: %v", err)
		http.Error(w, "Could not connect to target server", http.StatusServiceUnavailable)
		return
	}
	defer serverConn.Close()

	// Upgrade the client connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading client WebSocket connection: %v", err)
		return
	}
	defer clientConn.Close()

	// Goroutine to proxy messages from client to server
	go func() {
		defer clientConn.Close()
		defer serverConn.Close()
		for {
			messageType, p, err := clientConn.ReadMessage()
			if err != nil {
				break
			}
			if err := serverConn.WriteMessage(messageType, p); err != nil {
				log.Printf("Server write error: %v", err)
				break
			}
		}
	}()

	// Proxy messages from server to client in the main goroutine
	for {
		messageType, p, err := serverConn.ReadMessage()
		if err != nil {
			break
		}
		if err := clientConn.WriteMessage(messageType, p); err != nil {
			log.Printf("Client write error: %v", err)
			break
		}
	}
}

// handleSchemaProxy proxies HTTP GET requests for schema.json files.
func handleSchemaProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
		return
	}

	log.Printf("Proxying schema request to: %s", targetURL)

	resp, err := http.Get(targetURL)
	if err != nil {
		log.Printf("Error performing proxy request: %v", err)
		http.Error(w, "Failed to fetch from target", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy headers from the target response to our response
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write the status code and copy the body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
