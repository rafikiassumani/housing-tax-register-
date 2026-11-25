package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type Assessment struct {
	ID         int64  `json:"id"`
	Parcel     string `json:"parcel"`
	Address    string `json:"address"`
	Year       int64  `json:"year"`
	ValueCents int64  `json:"valueCents"`
	RateBP     int64  `json:"rateBP"`
	TaxCents   int64  `json:"taxCents"`
	PaidCents  int64  `json:"paidCents"`
}

func save(path string, records []Assessment) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".assessments-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return false
	}
	return true
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print(err)
	}
}

func main() {
	const path = "assessments.json"
	records := []Assessment{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &records); err != nil {
			log.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatal(err)
	}
	var mutex sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/assessments", func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		sendJSON(w, http.StatusOK, records)
	})
	mux.HandleFunc("POST /api/assessments", func(w http.ResponseWriter, r *http.Request) {
		var item Assessment
		if !decode(w, r, &item) {
			return
		}
		item.Parcel = strings.ToUpper(strings.TrimSpace(item.Parcel))
		item.Address = strings.TrimSpace(item.Address)
		if item.Parcel == "" || item.Address == "" || item.Year < 1900 ||
			item.Year > 2200 || item.ValueCents < 1 || item.ValueCents > 100_000_000_000 ||
			item.RateBP < 0 || item.RateBP > 10_000 {
			http.Error(w, "Enter a parcel, address, valid year, assessed value, and rate from 0% to 100%.", 400)
			return
		}

		mutex.Lock()
		defer mutex.Unlock()
		item.ID = 1
		for _, existing := range records {
			if existing.Parcel == item.Parcel && existing.Year == item.Year {
				http.Error(w, "This parcel already has an assessment for that year.", 409)
				return
			}
			item.ID = max(item.ID, existing.ID+1)
		}
		item.TaxCents = (item.ValueCents*item.RateBP + 5000) / 10000
		item.PaidCents = 0
		next := append(slices.Clone(records), item)
		if err := save(path, next); err != nil {
			log.Print(err)
			http.Error(w, "Could not save assessment.", 500)
			return
		}
		records = next
		sendJSON(w, http.StatusCreated, item)
	})
	mux.HandleFunc("POST /api/payments", func(w http.ResponseWriter, r *http.Request) {
		var payment struct {
			ID    int64 `json:"id"`
			Cents int64 `json:"cents"`
		}
		if !decode(w, r, &payment) {
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		index := slices.IndexFunc(records, func(item Assessment) bool { return item.ID == payment.ID })
		if index < 0 {
			http.Error(w, "Assessment not found.", 404)
			return
		}
		item := records[index]
		if payment.Cents < 1 || payment.Cents > item.TaxCents-item.PaidCents {
			http.Error(w, "Payment must be positive and within the outstanding balance.", 400)
			return
		}
		item.PaidCents += payment.Cents
		next := slices.Clone(records)
		next[index] = item
		if err := save(path, next); err != nil {
			log.Print(err)
			http.Error(w, "Could not save payment.", 500)
			return
		}
		records = next
		sendJSON(w, http.StatusOK, item)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	log.Println("Housing Tax Register at http://127.0.0.1:3000")
	log.Fatal(http.ListenAndServe("127.0.0.1:3000", mux))
}
