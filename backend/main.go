package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on process environment")
	}

	apiKey := os.Getenv("LITELLM_API_KEY")
	model := os.Getenv("LITELLM_MODEL")
	if apiKey == "" || model == "" {
		log.Fatal("LITELLM_API_KEY and LITELLM_MODEL must be set")
	}

	baseURL := os.Getenv("LITELLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://llm.effective.land"
	}

	client := NewLiteLLMClient(baseURL, apiKey, model)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/estimate", estimateHandler(client))
	mux.HandleFunc("/api/compare", compareHandler(client))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("backend listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, withCORS(mux)))
}

// withCORS allows the local Vite dev server to call the API directly.
// The frontend never receives or forwards the LiteLLM API key.
func withCORS(next http.Handler) http.Handler {
	allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:5173"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
