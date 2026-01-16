package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type Result struct { // Struct untuk menyimpan hasil setiap request
	StatusCode int
	Duration   time.Duration
	Error      error
}

func main() {
	// Parsing command-line arguments
	url := flag.String("url", "http://localhost:8080", "Target URL to test")
	requests := flag.Int("n", 100, "Total number of requests")
	concurrency := flag.Int("c", 10, "Number of concurrent goroutines")
	timeout := flag.Duration("timeout", 30*time.Second, "Request timeout")
	verbose := flag.Bool("v", false, "Enable verbose output to show detailed individual request results (duration, etc.)")
	flag.Parse()

	// Validasi input
	if *requests <= 0 || *concurrency <= 0 { // pastikan requests dan concurrency positif
		fmt.Println("Error: requests and concurrency must be positive integers")
		return
	}

	// Setup HTTP client dengan konfigurasi aman dan dioptimalkan untuk throughput tinggi
	client := &http.Client{ // Client HTTP dengan timeout dan transport yang dioptimalkan
		Timeout: *timeout, // Set timeout sesuai argumen
		Transport: &http.Transport{ // Transport untuk koneksi yang efisien dan reuse maksimal
			MaxIdleConns:          1000,             // Tingkatkan maksimum koneksi idle untuk handle lebih banyak reuse
			MaxIdleConnsPerHost:   1000,             // Tingkatkan maksimum koneksi idle per host untuk throughput lebih tinggi
			MaxConnsPerHost:       1000,             // Batasi tapi tingkatkan max koneksi per host untuk cegah bottleneck
			IdleConnTimeout:       90 * time.Second, // Timeout untuk koneksi idle
			TLSHandshakeTimeout:   10 * time.Second, // Optimasi TLS handshake
			ExpectContinueTimeout: 1 * time.Second,  // Optimasi untuk request dengan body (walaupun GET)
			DisableCompression:    false,            // Biarkan compression on untuk efisiensi bandwidth jika server support
		},
	}

	// Channel untuk koordinasi
	jobs := make(chan int, *requests)       // Channel untuk job, gunakan int untuk track request index jika perlu
	results := make(chan Result, *requests) // Channel untuk hasil
	var wg sync.WaitGroup                   // WaitGroup untuk menunggu semua goroutine selesai

	// Worker pool
	for i := 0; i < *concurrency; i++ { // Mulai goroutine sesuai level concurrency
		wg.Add(1)   // Tambah ke WaitGroup
		go func() { // Worker goroutine
			defer wg.Done()              // Pastikan menandai selesai saat goroutine berakhir
			for reqIndex := range jobs { // Terima job dari channel, dengan index untuk logging opsional
				start := time.Now() // Catat waktu mulai

				req, err := http.NewRequest("GET", *url, nil) // Buat request baru (creation cepat, tidak perlu pool)
				if err != nil {                               // Tangani error pembuatan request
					results <- Result{Error: err} // Kirim ke channel hasil
					continue                      // Lanjutkan ke job berikutnya
				}

				resp, err := client.Do(req)
				duration := time.Since(start)

				if err != nil {
					results <- Result{Error: err, Duration: duration}
					continue
				}

				// Selalu tampilkan HTTP status code ke terminal
				log.Printf("Request %d: HTTP Status Code %d\n", reqIndex+1, resp.StatusCode)

				// Pastikan body selalu ditutup dengan efisien
				// Untuk optimasi throughput, baca body minimal: gunakan io.CopyN dengan limit jika body besar, tapi untuk load test sederhana, discard full
				_, _ = io.Copy(io.Discard, resp.Body) // Buang response body
				resp.Body.Close()

				results <- Result{
					StatusCode: resp.StatusCode,
					Duration:   duration,
				}
			}
		}()
	}

	// Kirim jobs dengan index
	go func() {
		for i := 0; i < *requests; i++ {
			jobs <- i
		}
		close(jobs)
	}()

	// Gunakan WaitGroup terpisah untuk prosesor hasil agar kita dapat mencetak ringkasan setelah semua hasil diproses.
	var processingWg sync.WaitGroup
	processingWg.Add(1)

	// Goroutine untuk memproses hasil secara real-time
	go func() {
		defer processingWg.Done()
		var (
			success, failed int
			totalTime       time.Duration
		)

		for r := range results {
			if r.Error != nil {
				failed++
				if *verbose {
					log.Printf("[FAIL] Request error: %v (Duration: %s)\n", r.Error, r.Duration.Round(time.Millisecond))
				}
				continue
			}

			if r.StatusCode >= 200 && r.StatusCode < 300 {
				success++
				totalTime += r.Duration
				if *verbose {
					log.Printf("[SUCCESS] Status: %d (Duration: %s)\n", r.StatusCode, r.Duration.Round(time.Millisecond))
				}
			} else {
				failed++
				if *verbose {
					log.Printf("[FAIL] Status: %d (Duration: %s)\n", r.StatusCode, r.Duration.Round(time.Millisecond))
				}
			}
		}

		// Hitung statistik akhir
		var avgTime time.Duration
		if success > 0 {
			avgTime = totalTime / time.Duration(success)
		}
		successRate := float64(success) / float64(*requests) * 100

		// Tampilkan hasil
		fmt.Printf("\n===== Go Flooder =====\n")
		fmt.Printf("Target URL:        %s\n", *url)
		fmt.Printf("Total Requests:    %d\n", *requests)
		fmt.Printf("Concurrency Level: %d\n", *concurrency)
		fmt.Printf("Successful (2xx):  %d (%.2f%%)\n", success, successRate)
		fmt.Printf("Failed:            %d\n", failed)
		if success > 0 {
			fmt.Printf("Avg Response Time: %v\n", avgTime.Round(time.Millisecond))
		}
		fmt.Println("=============================")
	}()

	wg.Wait()
	close(results)
	processingWg.Wait()
}
