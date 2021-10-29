package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	alertTriggeredKey    = "alert:triggered"
	alertBodyKey         = "alert:body"
	alertResolvedBodyKey = "alert:resolvebody"
	alertURLKey          = "alert:url"
	alertReceivedKey     = "alert:received"

	loopInterval = 1 * time.Minute
)

var (
	ctx = context.Background()
	rdb = redis.NewClient(&redis.Options{
		Addr:     "deadmanssnitch-redis:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
)

func httpRequest(url string, body io.Reader) error {
	client := &http.Client{}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "deadmanssnitch")
	req.Header.Set("Content-Type", "application/json")

	res, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}

	if res.StatusCode >= 400 {
		return fmt.Errorf("call failed with status: %v", res.Status)
	}

	return nil
}

func triggerAlert() error {
	val, err := rdb.Get(ctx, alertTriggeredKey).Result()
	if err != nil {
		return err
	}

	if val == "true" {
		return nil
	}

	url, err := rdb.Get(ctx, alertURLKey).Result()
	if err != nil {
		return fmt.Errorf("alert url key not set: %v", err)
	}

	body, err := rdb.Get(ctx, alertBodyKey).Result()
	if err != nil {
		return fmt.Errorf("alert body key not set: %v", err)
	}

	if err = httpRequest(url, strings.NewReader(body)); err != nil {
		return fmt.Errorf("http request failed: %v", err)
	}

	log.Print("triggered alert")
	return rdb.Set(ctx, alertTriggeredKey, "true", 0).Err()
}

func resolveAlert() error {
	val, err := rdb.Get(ctx, alertTriggeredKey).Result()
	if err != nil {
		return err
	}

	if val == "false" {
		return nil
	}

	url, err := rdb.Get(ctx, alertURLKey).Result()
	if err != nil {
		return fmt.Errorf("alert url key not set: %v", err)
	}

	body, err := rdb.Get(ctx, alertResolvedBodyKey).Result()
	if err != nil {
		return fmt.Errorf("alert body key not set: %v", err)
	}

	if err = httpRequest(url, strings.NewReader(body)); err != nil {
		return fmt.Errorf("http request failed: %v", err)
	}

	log.Print("resolved alert")
	return rdb.Set(ctx, alertTriggeredKey, "false", 0).Err()
}

func controller() {
	for {
		time.Sleep(loopInterval)

		_, err := rdb.Get(ctx, alertReceivedKey).Result()

		if err == redis.Nil {
			if err := triggerAlert(); err != nil {
				log.Printf("failed to trigger alert: %v", err)
			}
		} else if err != nil {
			log.Printf("failed to read from redis: %v", err)
		} else {
			if err := resolveAlert(); err != nil {
				log.Printf("failed to resolve alert: %v", err)
			}
		}
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
}

func alertHandler(w http.ResponseWriter, r *http.Request) {
	err := rdb.Set(ctx, alertReceivedKey, "true", loopInterval).Err()
	if err != nil {
		log.Printf("failed to write to redis: %v", err)
	}
}

func main() {
	if err := rdb.Set(ctx, alertTriggeredKey, "false", 0); err != nil {
		log.Fatalf("couldn't connect redis: %v", err)
	}

	go controller()

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/alert", alertHandler)

	log.Fatal(http.ListenAndServe(":9090", nil))
}
