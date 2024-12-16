// main.go
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

// TokenUsage struct corresponds to your database model
type TokenUsage struct {
	ID          int       `json:"id"`
	Date        time.Time `json:"date"`
	Model       string    `json:"model"`
	TotalTokens int       `json:"total_tokens"`
}

var db *sql.DB

func main() {
	godotenv.Load() // Load .env file
	// Database connection
	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		log.Fatal("DATABASE_URL environment variable not set")
		return
	}

	//Retry connection logic
	maxRetries := 5
	retryDelay := 2 * time.Second
	var err error
	for i := 0; i < maxRetries; i++ {
		db, err = sql.Open("postgres", dbUrl)
		if err != nil {
			log.Printf("Failed to connect to the database: %v, retrying in %v", err, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2
			continue
		}
		err = db.Ping()
		if err != nil {
			log.Printf("Failed to ping database: %v, retrying in %v", err, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2
			db.Close()
			continue
		}
		log.Println("Database connection successful")
		break // Break if successful
	}
	if err != nil {
		log.Fatal("Failed to connect to the database after multiple retries:", err)
		return
	}
	defer db.Close()

	// Ensure the table exists (using raw SQL)
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS token_usage (
            id SERIAL PRIMARY KEY,
            date DATE NOT NULL,
            model VARCHAR(255) NOT NULL,
            total_tokens INTEGER NOT NULL
        );
    `)
	if err != nil {
		log.Fatal("Error creating table:", err)
		return
	}
	fmt.Println("Table created if not present")

	router := mux.NewRouter()
	router.HandleFunc("/token_usage", recordTokenUsage).Methods("POST")
	router.HandleFunc("/token_usage", getTokenUsageAll).Methods("GET")
	router.HandleFunc("/token_usage/{date}/{model}", getTokenUsageByDateAndModel).Methods("GET")
	router.HandleFunc("/token_usage/{model}/{period}", getTokenUsageByPeriod).Methods("GET")

	log.Println("Server listening on port 5001")
	http.ListenAndServe(":5001", router)
}

func recordTokenUsage(w http.ResponseWriter, r *http.Request) {
	var usage TokenUsage
	if err := json.NewDecoder(r.Body).Decode(&usage); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload", err)
		return
	}
	fmt.Printf("Received token usage on %s for %s with %d\n", usage.Date.Format("2006-01-02"), usage.Model, usage.TotalTokens)

	// Check if there's a record for the date and model
	var existingID int
	err := db.QueryRow("SELECT id FROM token_usage WHERE date = $1 AND model = $2", usage.Date, usage.Model).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		respondError(w, http.StatusInternalServerError, "Database query error", err)
		return
	}
	if err == sql.ErrNoRows { // No record exists for this date and model
		_, err = db.Exec("INSERT INTO token_usage (date, model, total_tokens) VALUES ($1, $2, $3)", usage.Date, usage.Model, usage.TotalTokens)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to insert token usage", err)
			return
		}
		fmt.Printf("Recorded token usage on %s for %s with %d\n", usage.Date.Format("2006-01-02"), usage.Model, usage.TotalTokens)
		respondJSON(w, http.StatusCreated, map[string]string{"message": "Token usage recorded successfully"})
	} else { // Record exists, update
		_, err = db.Exec("UPDATE token_usage SET total_tokens = $1 WHERE id = $2", usage.TotalTokens, existingID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to update token usage", err)
			return
		}
		fmt.Printf("Updated token usage on %s for %s with %d\n", usage.Date.Format("2006-01-02"), usage.Model, usage.TotalTokens)
		respondJSON(w, http.StatusOK, map[string]string{"message": "Token usage updated successfully"})
	}
}

func getTokenUsageAll(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, date, model, total_tokens FROM token_usage")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database query error", err)
		return
	}
	defer rows.Close()

	var usages []TokenUsage
	for rows.Next() {
		var usage TokenUsage
		if err := rows.Scan(&usage.ID, &usage.Date, &usage.Model, &usage.TotalTokens); err != nil {
			respondError(w, http.StatusInternalServerError, "Error scanning row", err)
			return
		}
		usages = append(usages, usage)
	}
	if err = rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "Error reading data", err)
		return
	}
	respondJSON(w, http.StatusOK, usages)
}

func getTokenUsageByDateAndModel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	dateStr := vars["date"]
	model := vars["model"]
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid date format", err)
		return
	}
	var totalTokens int
	err = db.QueryRow("SELECT total_tokens FROM token_usage WHERE date = $1 AND model = $2", date, model).Scan(&totalTokens)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusOK, map[string]interface{}{"message": "No token usage data found for this date and model", "status": 0})
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Database query error", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"total_tokens": totalTokens, "status": 1})

}

func getTokenUsageByPeriod(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	model := vars["model"]
	period := vars["period"]
	var startDate time.Time
	today := time.Now().Truncate(24 * time.Hour)
	switch period {
	case "week":
		startDate = today.AddDate(0, 0, -int(today.Weekday()))
	case "month":
		startDate = today.AddDate(0, 0, -today.Day()+1)
	case "lifetime":
		startDate = time.Time{}
	default:
		respondJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid period. Use 'week', 'month' or 'lifetime'"})
		return
	}
	var totalTokens int
	var err error
	if !startDate.IsZero() {
		err = db.QueryRow("SELECT COALESCE(SUM(total_tokens), 0) FROM token_usage WHERE model = $1 AND date >= $2", model, startDate).Scan(&totalTokens)
	} else {
		err = db.QueryRow("SELECT COALESCE(SUM(total_tokens), 0) FROM token_usage WHERE model = $1", model).Scan(&totalTokens)
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database query error", err)
		return
	}
	if totalTokens == 0 {
		respondJSON(w, http.StatusNotFound, map[string]string{"message": "No token usage data found for this model"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]int{"total_tokens": totalTokens})
}
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}
func respondError(w http.ResponseWriter, status int, message string, err error) {
	log.Printf("%s : %v", message, err)
	respondJSON(w, status, map[string]string{"message": message, "error": err.Error()})
}
