package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	client     *mongo.Client
	usersColl  *mongo.Collection
	
	// Leaderboard caching with mutex for concurrent access
	leaderboardCache     []map[string]interface{}
	leaderboardCacheMux  sync.RWMutex
	leaderboardCacheTime time.Time
	
	// User data prefetch cache for faster level loading
	userPrefetchCache    map[string]*User
	userPrefetchCacheMux sync.RWMutex
	
	// Challenge lookup map for O(1) access
	challengeByLevel map[int]map[string]interface{}
	challengeByFlag  map[string]map[string]interface{}
	
	challenges = []map[string]interface{}{
		{"level": 1, "flag": "WLUG{ARYP1589}", "points": 100},
		{"level": 2, "flag": "WLUG{ARYP1589}", "points": 150},
		{"level": 3, "flag": "WLUG{HYGT5489}", "points": 200},
		{"level": 4, "flag": "WLUG{AYVY2014}", "points": 250},
		{"level": 5, "flag": "WLUG{ASTK1230}", "points": 300},
		{"level": 6, "flag": "WLUG{VIVA9563}", "points": 350},
		{"level": 7, "flag": "WLUG{1721702}", "points": 400},
		{"level": 8, "flag": "flag{web_2_0}", "points": 450},
		{"level": 9, "flag": "flag{network_ninja}", "points": 500},
		{"level": 10, "flag": "flag{ctf_mastery}", "points": 1000},
	}
)

type User struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Username    string             `bson:"username" json:"username"`
	Password    string             `bson:"password" json:"password"`
	Score       int                `bson:"score" json:"score"`
	SolvedLevels []int             `bson:"solvedLevels" json:"solvedLevels"`
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getCurrentLevelForUser(user *User) int {
	if user == nil || len(user.SolvedLevels) == 0 {
		return 1
	}
	max := 0
	for _, v := range user.SolvedLevels {
		if v > max {
			max = v
		}
	}
	return max + 1
}

// initChallengeMaps creates lookup maps for O(1) challenge access
func initChallengeMaps() {
	challengeByLevel = make(map[int]map[string]interface{})
	challengeByFlag = make(map[string]map[string]interface{})
	
	for _, c := range challenges {
		level := c["level"].(int)
		flag := c["flag"].(string)
		challengeByLevel[level] = c
		challengeByFlag[flag] = c
	}
}

// prefetchUserData asynchronously fetches and caches user data
func prefetchUserData(userId string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		
		var user User
		err := usersColl.FindOne(ctx, bson.M{"username": userId}).Decode(&user)
		if err != nil {
			return // silently fail for prefetch
		}
		
		userPrefetchCacheMux.Lock()
		if userPrefetchCache == nil {
			userPrefetchCache = make(map[string]*User)
		}
		userPrefetchCache[userId] = &user
		userPrefetchCacheMux.Unlock()
	}()
}

// getCachedUser retrieves user from cache or DB
func getCachedUser(ctx context.Context, userId string) (*User, error) {
	// Try cache first
	userPrefetchCacheMux.RLock()
	if cachedUser, ok := userPrefetchCache[userId]; ok {
		userPrefetchCacheMux.RUnlock()
		// Return a copy to avoid data races
		userCopy := *cachedUser
		return &userCopy, nil
	}
	userPrefetchCacheMux.RUnlock()
	
	// Not in cache, fetch from DB
	var user User
	err := usersColl.FindOne(ctx, bson.M{"username": userId}).Decode(&user)
	if err != nil {
		return nil, err
	}
	
	// Update cache for next time
	go func() {
		userPrefetchCacheMux.Lock()
		if userPrefetchCache == nil {
			userPrefetchCache = make(map[string]*User)
		}
		userPrefetchCache[userId] = &user
		userPrefetchCacheMux.Unlock()
	}()
	
	return &user, nil
}

// invalidateUserCache removes user from cache after updates
func invalidateUserCache(userId string) {
	userPrefetchCacheMux.Lock()
	delete(userPrefetchCache, userId)
	userPrefetchCacheMux.Unlock()
}

func apiTestHandler(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("CTF API is up and running!"))
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Username == "" || body.Password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user User
	err := usersColl.FindOne(ctx, bson.M{"username": body.Username}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Invalid username or password"})
		return
	} else if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	if user.Password != body.Password {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Invalid username or password"})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "username": user.Username})
}

func getLevelHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	userId := q.Get("userId")
	if userId == "" {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to get user from cache first
	user, err := getCachedUser(ctx, userId)
	if err == mongo.ErrNoDocuments {
		// Create new user
		newUser := User{Username: userId, Score: 0, SolvedLevels: []int{}}
		_, err = usersColl.InsertOne(ctx, newUser)
		if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
		user = &newUser
		// Cache the new user
		prefetchUserData(userId)
	} else if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	level := getCurrentLevelForUser(user)
	json.NewEncoder(w).Encode(map[string]int{"level": level})
}

func checkFlagHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserId string `json:"userId"`
		Flag   string `json:"flag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.UserId == "" || body.Flag == "" {
		http.Error(w, "userId and flag are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use challenge lookup map for O(1) access
	found, exists := challengeByFlag[body.Flag]
	if !exists {
		// Fetch user to get current level for incorrect flag response
		user, _ := getCachedUser(ctx, body.UserId)
		currentLevel := getCurrentLevelForUser(user)
		json.NewEncoder(w).Encode(map[string]interface{}{"correct": false, "newLevel": currentLevel})
		return
	}

	levelNum := int(found["level"].(int))
	points := int(found["points"].(int))

	// Only push level + add score if the user hasn't already solved this level.
	// Use a plain UpdateOne (no upsert) to avoid operator conflicts between
	// $push/$inc and $setOnInsert when the document doesn't exist yet.
	filter := bson.M{
		"username":     body.UserId,
		"solvedLevels": bson.M{"$ne": levelNum},
	}
	update := bson.M{
		"$push": bson.M{"solvedLevels": levelNum},
		"$inc":  bson.M{"score": points},
	}

	result, err := usersColl.UpdateOne(ctx, filter, update)
	if err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	// Invalidate cache after update
	invalidateUserCache(body.UserId)

	// Fetch user after the update to return correct newLevel
	var user User
	err = usersColl.FindOne(ctx, bson.M{"username": body.UserId}).Decode(&user)
	if err != nil && err != mongo.ErrNoDocuments {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	newLevel := getCurrentLevelForUser(&user)

	// If the flag was actually new (not a duplicate submission), prefetch next level data
	if result.ModifiedCount > 0 {
		// Asynchronously prefetch user data for the next request
		go prefetchUserData(body.UserId)
		
		// Log successful flag submission for analytics
		go func() {
			log.Printf("User %s solved level %d, advancing to level %d", body.UserId, levelNum, newLevel)
		}()
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"correct": true, "newLevel": newLevel})
}

func resetUserHandler(w http.ResponseWriter, r *http.Request) {
	var body struct{ UserId string `json:"userId"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.UserId == "" {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := usersColl.FindOneAndUpdate(ctx, bson.M{"username": body.UserId}, bson.M{"$set": bson.M{"score": 0, "solvedLevels": []int{}}})
	if res.Err() == mongo.ErrNoDocuments {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	} else if res.Err() != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	
	// Invalidate cache after reset
	invalidateUserCache(body.UserId)
	
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	var body struct{ UserId string `json:"userId"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.UserId == "" {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := usersColl.DeleteOne(ctx, bson.M{"username": body.UserId})
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"deleted": res.DeletedCount > 0})
}

func leaderboardHandler(w http.ResponseWriter, r *http.Request) {
	// Try to use cached leaderboard if it's fresh (< 10 seconds old)
	leaderboardCacheMux.RLock()
	if time.Since(leaderboardCacheTime) < 10*time.Second && leaderboardCache != nil {
		cache := leaderboardCache
		leaderboardCacheMux.RUnlock()
		json.NewEncoder(w).Encode(cache)
		return
	}
	leaderboardCacheMux.RUnlock()

	// Cache is stale or empty, fetch from DB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := usersColl.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"score": -1}).SetLimit(100))
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)
	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	// convert to pure json-safe slice
	out := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]interface{}{"username": u.Username, "score": u.Score, "solvedLevels": u.SolvedLevels})
	}

	// Update cache
	leaderboardCacheMux.Lock()
	leaderboardCache = out
	leaderboardCacheTime = time.Now()
	leaderboardCacheMux.Unlock()

	json.NewEncoder(w).Encode(out)
}

func challengesHandler(w http.ResponseWriter, _ *http.Request) {
	out := make([]map[string]interface{}, 0, len(challenges))
	for _, c := range challenges {
		out = append(out, map[string]interface{}{"level": c["level"], "points": c["points"]})
	}
	json.NewEncoder(w).Encode(out)
}

func connectDB(uri string) (*mongo.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientOpts := options.Client().ApplyURI(uri)
	cl, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, err
	}
	return cl, nil
}

// createIndexes creates database indexes for better query performance
func createIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "username", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "score", Value: -1}},
		},
	}
	_, err := coll.Indexes().CreateMany(ctx, indexes)
	return err
}

// refreshLeaderboardCache periodically updates the leaderboard cache in the background
func refreshLeaderboardCache(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cursor, err := usersColl.Find(fetchCtx, bson.M{}, options.Find().SetSort(bson.M{"score": -1}).SetLimit(100))
			if err != nil {
				cancel()
				log.Printf("failed to refresh leaderboard cache: %v", err)
				continue
			}

			var users []User
			if err := cursor.All(fetchCtx, &users); err != nil {
				cursor.Close(fetchCtx)
				cancel()
				log.Printf("failed to decode leaderboard users: %v", err)
				continue
			}
			cursor.Close(fetchCtx)
			cancel()

			out := make([]map[string]interface{}, 0, len(users))
			for _, u := range users {
				out = append(out, map[string]interface{}{"username": u.Username, "score": u.Score, "solvedLevels": u.SolvedLevels})
			}

			leaderboardCacheMux.Lock()
			leaderboardCache = out
			leaderboardCacheTime = time.Now()
			leaderboardCacheMux.Unlock()

			log.Printf("leaderboard cache refreshed with %d users", len(out))
		}
	}
}

func main() {
	portStr := os.Getenv("PORT")
	if portStr == "" {
		portStr = "10000"
	}
	port, _ := strconv.Atoi(portStr)
	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		log.Fatal("MONGODB_URI must be set in the environment")
	}

	var err error
	client, err = connectDB(mongoURI)
	if err != nil {
		log.Fatalf("failed to connect to mongo: %v", err)
	}
	usersColl = client.Database("ctf_db").Collection("users")

	// Create database indexes for better performance
	idxCtx, idxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := createIndexes(idxCtx, usersColl); err != nil {
		log.Printf("warning: failed to create indexes: %v", err)
	}
	idxCancel()

	// Initialize challenge lookup maps
	initChallengeMaps()

	// Context for graceful shutdown
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Start background leaderboard cache refresh (every 5 seconds)
	go refreshLeaderboardCache(shutdownCtx, 5*time.Second)
	log.Println("background leaderboard cache refresh started")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/test", apiTestHandler)
	mux.HandleFunc("/login", loginHandler)
	mux.HandleFunc("/getLevel", getLevelHandler)
	mux.HandleFunc("/checkFlag", checkFlagHandler)
	mux.HandleFunc("/resetUser", resetUserHandler)
	mux.HandleFunc("/deleteUser", deleteUserHandler)
	mux.HandleFunc("/api/leaderboard", leaderboardHandler)
	mux.HandleFunc("/api/challenges", challengesHandler)

	handler := withCORS(mux)
	addr := ":" + strconv.Itoa(port)
	
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Channel to listen for interrupt signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		log.Printf("listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-stop
	log.Println("shutting down server gracefully...")

	// Cancel background tasks
	shutdownCancel()

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	// Disconnect from MongoDB
	if err := client.Disconnect(ctx); err != nil {
		log.Printf("mongodb disconnect error: %v", err)
	}

	log.Println("server stopped")
}
