package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/davecgh/go-spew/spew"

	webhookv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/schedulerwebhook/v1alpha1"
)

var (
	port = flag.Int("port", 50051, "The server port")
)

func doFilter(req *webhookv1a1.FilterRequest) *webhookv1a1.FilterResponse {
	log.Printf("Received filter request\nScheduling unit: %s: Cluster: %s\n", spew.Sdump(req.SchedulingUnit), spew.Sdump(req.Cluster))

	return &webhookv1a1.FilterResponse{
		// This plugin filters out clusters with the digit '1' in their name
		Selected: !strings.Contains(req.Cluster.Name, "1"),
		Error:    "",
	}
}

func doScore(req *webhookv1a1.ScoreRequest) *webhookv1a1.ScoreResponse {
	log.Printf("Received score request\nScheduling unit: %s: Cluster: %s\n", spew.Sdump(req.SchedulingUnit), spew.Sdump(req.Cluster))

	return &webhookv1a1.ScoreResponse{
		// this score plugin scores clusters based on the number of characters in their name
		Score: int64(len(req.Cluster.Name)),
		Error: "",
	}
}

func doSelect(req *webhookv1a1.SelectRequest) *webhookv1a1.SelectResponse {
	log.Printf("Received select request\nScheduling unit: %s: Scores: %s\n", spew.Sdump(req.SchedulingUnit), spew.Sdump(req.ClusterScores))

	// this score plugin always removes the lowest scoring cluster
	lowestScoreIdx := 0
	lowestScore := int64(math.MaxInt64)
	for i, score := range req.ClusterScores {
		if score.Score < lowestScore {
			lowestScore = score.Score
			lowestScoreIdx = i
		}
	}

	selectedClusters := []string{}
	for i, score := range req.ClusterScores {
		if i == lowestScoreIdx {
			continue
		}
		selectedClusters = append(selectedClusters, score.Cluster.Name)
	}

	return &webhookv1a1.SelectResponse{
		SelectedClusterNames: selectedClusters,
		Error:                "",
	}
}

func processRequest[Req any, Resp any](
	httpRespWriter http.ResponseWriter,
	httpReq *http.Request,
	handler func(req *Req) *Resp,
) {
	// Validate and decode the requeset
	if httpReq.Method != http.MethodPost {
		http.Error(httpRespWriter, "only POST requests are allowed", http.StatusMethodNotAllowed)
		return
	}

	req := new(Req)
	if err := json.NewDecoder(httpReq.Body).Decode(req); err != nil {
		http.Error(httpRespWriter, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
		return
	}

	// Run the scheduling logic
	resp := handler(req)

	// Encode and send the response
	respBytes, err := json.Marshal(resp)
	if err != nil {
		http.Error(httpRespWriter, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}

	httpRespWriter.Header().Set("Content-Type", "application/json")
	httpRespWriter.Write(respBytes)
}

func main() {
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/filter", func(w http.ResponseWriter, r *http.Request) {
		processRequest(w, r, doFilter)
	})
	mux.HandleFunc("/score", func(w http.ResponseWriter, r *http.Request) {
		processRequest(w, r, doScore)
	})
	mux.HandleFunc("/select", func(w http.ResponseWriter, r *http.Request) {
		processRequest(w, r, doSelect)
	})

	log.Printf("Starting server on port %d", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			log.Printf("Server shutdown")
		} else {
			log.Fatalf("Failed to start server: %v", err)
		}
	}
}
