package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	client     *mongo.Client
	usersColl  *mongo.Collection
	challenges = []map[string]interface{}{
		{"level": 1, "flag": "flag{welcome_to_the_game}", "points": 100},
		{"level": 2, "flag": "flag{docker_is_fun}", "points": 150},
		{"level": 3, "flag": "flag{sql_mastery_achieved}", "points": 200},
		{"level": 4, "flag": "flag{reverse_engineering}", "points": 250},
		{"level": 5, "flag": "flag{crypto_beginner}", "points": 300},
		{"level": 6, "flag": "flag{forensics_time}", "points": 350},
		{"level": 7, "flag": "flag{pwn_it}", "points": 400},
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

func apiTestHandler(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("CTF API is up and running!"))
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

	var user User
	err := usersColl.FindOne(ctx, bson.M{"username": userId}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		user = User{Username: userId, Score: 0, SolvedLevels: []int{}}
		_, err = usersColl.InsertOne(ctx, user)
		if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	level := getCurrentLevelForUser(&user)
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

	// ensure user exists
	var user User
	err := usersColl.FindOne(ctx, bson.M{"username": body.UserId}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		user = User{Username: body.UserId, Score: 0, SolvedLevels: []int{}}
		_, err = usersColl.InsertOne(ctx, user)
		if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// find challenge by flag
	var found map[string]interface{}
	for _, c := range challenges {
		if c["flag"] == body.Flag {
			found = c
			break
		}
	}

	currentLevel := getCurrentLevelForUser(&user)
	if found == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"correct": false, "newLevel": currentLevel})
		return
	}

	levelNum := int(found["level"].(int))
	// if already solved
	for _, lv := range user.SolvedLevels {
		if lv == levelNum {
			json.NewEncoder(w).Encode(map[string]interface{}{"correct": true, "newLevel": getCurrentLevelForUser(&user)})
			return
		}
	}

	// update user: append level and add score
	user.SolvedLevels = append(user.SolvedLevels, levelNum)
	user.Score = user.Score + int(found["points"].(int))

	_, err = usersColl.UpdateOne(ctx, bson.M{"username": body.UserId}, bson.M{"$set": bson.M{"solvedLevels": user.SolvedLevels, "score": user.Score}})
	if err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"correct": true, "newLevel": getCurrentLevelForUser(&user)})
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

	mux := http.NewServeMux()
	mux.HandleFunc("/api/test", apiTestHandler)
	mux.HandleFunc("/getLevel", getLevelHandler)
	mux.HandleFunc("/checkFlag", checkFlagHandler)
	mux.HandleFunc("/resetUser", resetUserHandler)
	mux.HandleFunc("/deleteUser", deleteUserHandler)
	mux.HandleFunc("/api/leaderboard", leaderboardHandler)
	mux.HandleFunc("/api/challenges", challengesHandler)

	handler := withCORS(mux)
	addr := ":" + strconv.Itoa(port)
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
