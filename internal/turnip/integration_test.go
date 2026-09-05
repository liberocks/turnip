package turnip

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreAtomicOwnershipCapacityAndExpiry(t *testing.T) {
	redisURL := os.Getenv("TURNIP_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set TURNIP_TEST_REDIS_URL to run Redis integration tests")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	})
	store := NewRedisStore(client, NewMetrics())

	const contenders = 10
	results := make(chan JoinResult, contenders)
	errorsChannel := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, joinErr := store.Join(context.Background(), Identity{
				Subject: "subject-" + string(rune('a'+index)), RoomID: "race-room",
			}, "participant-"+string(rune('a'+index)), "instance", randomID())
			results <- result
			errorsChannel <- joinErr
		}(index)
	}
	wait.Wait()
	close(results)
	close(errorsChannel)
	owners := 0
	for result := range results {
		if result.Owner {
			owners++
		}
	}
	for joinErr := range errorsChannel {
		if joinErr != nil {
			t.Fatal(joinErr)
		}
	}
	if owners != 1 {
		t.Fatalf("atomic room creation produced %d owners", owners)
	}

	ownerSubject, err := client.HGet(context.Background(), roomMetaKey("race-room"), "owner").Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Disband(context.Background(), "race-room", "not-owner", "participant-b", "instance", randomID()); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner disband error = %v", err)
	}
	ownerParticipant := "participant-" + strings.TrimPrefix(ownerSubject, "subject-")
	if err := store.Leave(context.Background(), "race-room", ownerParticipant, "instance", randomID()); err != nil {
		t.Fatal(err)
	}
	if exists, _ := client.Exists(context.Background(), roomMetaKey("race-room")).Result(); exists != 1 {
		t.Fatal("room ownership disappeared while members remained")
	}
	reconnected, err := store.Join(context.Background(), Identity{Subject: ownerSubject, RoomID: "race-room"}, "owner-reconnected", "instance", randomID())
	if err != nil || !reconnected.Owner {
		t.Fatalf("owner reconnect = %+v, %v", reconnected, err)
	}
	if err := store.Disband(context.Background(), "race-room", ownerSubject, "owner-reconnected", "instance", randomID()); err != nil {
		t.Fatal(err)
	}

	for index := 0; index < MaxRoomConnections; index++ {
		id := "capacity-" + strconv.Itoa(index)
		if _, err := store.Join(context.Background(), Identity{Subject: id, RoomID: "capacity-room"}, id, "instance", randomID()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Join(context.Background(), Identity{Subject: "overflow", RoomID: "capacity-room"}, "overflow", "instance", randomID()); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("overflow join error = %v", err)
	}
	if err := store.Disband(context.Background(), "capacity-room", "capacity-0", "capacity-0", "instance", randomID()); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Join(context.Background(), Identity{Subject: "stale", RoomID: "stale-room"}, "stale-participant", "instance", randomID()); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(context.Background(), roomMembersKey("stale-room"), redis.Z{Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: "stale-participant"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(context.Background(), "sweeper"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Participant(context.Background(), "stale-room", "stale-participant"); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("stale participant lookup error = %v", err)
	}
	if member, _ := client.SIsMember(context.Background(), roomsKey, "stale-room").Result(); member {
		t.Fatal("empty stale room remained in the room index")
	}

	if _, err := store.Join(context.Background(), Identity{Subject: "old-owner", RoomID: "recreated-room"}, "old-owner-id", "instance", randomID()); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(context.Background(), roomMembersKey("recreated-room"), redis.Z{Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: "old-owner-id"}).Err(); err != nil {
		t.Fatal(err)
	}
	recreated, err := store.Join(context.Background(), Identity{Subject: "new-owner", RoomID: "recreated-room"}, "new-owner-id", "instance", randomID())
	if err != nil || !recreated.Owner {
		t.Fatalf("recreated room did not assign a new owner: %+v, %v", recreated, err)
	}
}

func TestReadinessReflectsRedisAndSubscription(t *testing.T) {
	redisURL := os.Getenv("TURNIP_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set TURNIP_TEST_REDIS_URL to run Redis integration tests")
	}
	config := validConfig()
	config.RedisURL = redisURL
	config.InstanceID = "readiness-test"
	config.MetricsAddr = ""
	app, err := NewApp(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		app.hub.Shutdown(shutdownCtx)
		_ = app.redis.Close()
	})

	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	app.mainHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready response = %d %s", response.Code, response.Body.String())
	}

	app.hub.ready.Store(false)
	response = httptest.NewRecorder()
	app.mainHandler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready response = %d %s", response.Code, response.Body.String())
	}

	app.hub.ready.Store(true)
	if err := app.redis.Close(); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	app.mainHandler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("Redis-down response = %d %s", response.Code, response.Body.String())
	}
}

func TestTwoInstanceTargetingOwnershipAndDisband(t *testing.T) {
	redisURL := os.Getenv("TURNIP_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set TURNIP_TEST_REDIS_URL to run Redis integration tests")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := redis.NewClient(options)
	if err := admin.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.FlushDB(context.Background()).Err()
		_ = admin.Close()
	})

	secret := strings.Repeat("integration-secret-", 3)
	auth := NewAuthenticator(secret, "turnip-tests", "turnip")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub1, client1 := integrationHub(t, ctx, options, auth, "instance-1")
	hub2, client2 := integrationHub(t, ctx, options, auth, "instance-2")
	server1 := httptest.NewServer(http.HandlerFunc(hub1.ServeWS))
	server2 := httptest.NewServer(http.HandlerFunc(hub2.ServeWS))
	t.Cleanup(func() {
		server1.Close()
		server2.Close()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		hub1.Shutdown(shutdownCtx)
		hub2.Shutdown(shutdownCtx)
		_ = client1.Close()
		_ = client2.Close()
	})

	ownerConn := dialTestClient(t, server1.URL, testToken(t, secret, "owner", "shared-room", "Owner"))
	ownerReady := readType[ReadyData](t, ownerConn, "ready")
	if !ownerReady.Owner || ownerReady.ParticipantID == "" {
		t.Fatalf("owner ready payload = %+v", ownerReady)
	}

	memberConn := dialTestClient(t, server2.URL, testToken(t, secret, "member", "shared-room", "Member"))
	memberReady := readType[ReadyData](t, memberConn, "ready")
	if memberReady.Owner || memberReady.ParticipantID == ownerReady.ParticipantID {
		t.Fatalf("member ready payload = %+v", memberReady)
	}
	joined := readType[Participant](t, ownerConn, "participant_joined")
	if joined.ID != memberReady.ParticipantID {
		t.Fatalf("joined participant = %+v", joined)
	}

	payload := json.RawMessage(`{"kind":"offer","sdp":"opaque"}`)
	writeClient(t, ownerConn, ClientEnvelope{
		ID: "signal-1", Type: "signal",
		Data: mustJSON(t, SignalRequest{To: memberReady.ParticipantID, Payload: payload}),
	})
	signal := readType[SignalData](t, memberConn, "signal")
	if signal.From != ownerReady.ParticipantID || signal.RequestID != "signal-1" || string(signal.Payload) != string(payload) {
		t.Fatalf("signal payload = %+v", signal)
	}

	writeClient(t, memberConn, ClientEnvelope{ID: "disband-1", Type: "disband_room", Data: json.RawMessage(`{}`)})
	forbidden := readType[ErrorData](t, memberConn, "error")
	if forbidden.Code != "forbidden" || forbidden.RequestID != "disband-1" {
		t.Fatalf("error payload = %+v", forbidden)
	}

	if err := ownerConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "leaving")); err != nil {
		t.Fatal(err)
	}
	left := readType[Participant](t, memberConn, "participant_left")
	if left.ID != ownerReady.ParticipantID || !left.Owner {
		t.Fatalf("left participant = %+v", left)
	}

	ownerReconnect := dialTestClient(t, server1.URL, testToken(t, secret, "owner", "shared-room", "Owner"))
	ownerReconnectReady := readType[ReadyData](t, ownerReconnect, "ready")
	if !ownerReconnectReady.Owner || ownerReconnectReady.ParticipantID == ownerReady.ParticipantID {
		t.Fatalf("reconnected owner payload = %+v", ownerReconnectReady)
	}
	_ = readType[Participant](t, memberConn, "participant_joined")

	writeClient(t, ownerReconnect, ClientEnvelope{ID: "disband-2", Type: "disband_room", Data: json.RawMessage(`{}`)})
	memberDisband := readType[map[string]string](t, memberConn, "room_disbanded")
	ownerDisband := readType[map[string]string](t, ownerReconnect, "room_disbanded")
	if memberDisband["by"] != ownerReconnectReady.ParticipantID || ownerDisband["reason"] != "owner_requested" {
		t.Fatalf("disband payloads = %+v / %+v", memberDisband, ownerDisband)
	}
	expectCloseCode(t, memberConn, closeDisbanded)
	expectCloseCode(t, ownerReconnect, closeDisbanded)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		keys, scanErr := admin.Keys(context.Background(), "turnip:v1:room:shared-room:*").Result()
		if scanErr != nil {
			t.Fatal(scanErr)
		}
		if len(keys) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("room state was not removed after disband")
}

func TestHubShutdownClosesConnectionsAndRemovesPresence(t *testing.T) {
	redisURL := os.Getenv("TURNIP_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set TURNIP_TEST_REDIS_URL to run Redis integration tests")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	metrics := NewMetrics()
	secret := strings.Repeat("shutdown-secret-", 3)
	hub := NewHub(NewRedisStore(client, metrics), NewAuthenticator(secret, "turnip-tests", "turnip"), metrics, "shutdown-instance", []string{"https://client.example"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	connection := dialTestClient(t, server.URL, testToken(t, secret, "user", "shutdown-room", "User"))
	_ = readType[ReadyData](t, connection, "ready")

	done := make(chan struct{})
	go func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		hub.Shutdown(shutdownCtx)
		close(done)
	}()
	expectCloseCode(t, connection, websocket.CloseGoingAway)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hub shutdown did not complete")
	}
	server.Close()
	if keys, err := client.Keys(context.Background(), "turnip:v1:room:shutdown-room:*").Result(); err != nil || len(keys) != 0 {
		t.Fatalf("presence remained after shutdown: %v, %v", keys, err)
	}
	_ = client.FlushDB(context.Background()).Err()
	_ = client.Close()
}

func integrationHub(t *testing.T, ctx context.Context, options *redis.Options, auth *Authenticator, instanceID string) (*Hub, *redis.Client) {
	t.Helper()
	client := redis.NewClient(options)
	metrics := NewMetrics()
	hub := NewHub(NewRedisStore(client, metrics), auth, metrics, instanceID, []string{"https://client.example"})
	if err := hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return hub, client
}

func testToken(t *testing.T, secret, subject, room, name string) string {
	t.Helper()
	token, err := GenerateToken(secret, "turnip-tests", "turnip", subject, room, name, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func dialTestClient(t *testing.T, serverURL, token string) *websocket.Conn {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Scheme = "ws"
	parsed.RawQuery = url.Values{"token": []string{token}}.Encode()
	connection, response, err := websocket.DefaultDialer.Dial(parsed.String(), http.Header{"Origin": []string{"https://client.example"}})
	if err != nil {
		if response != nil {
			t.Fatalf("dial failed with HTTP %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	connection.SetReadLimit(MaxMessageBytes)
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func readType[T any](t *testing.T, connection *websocket.Conn, expected string) T {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := connection.ReadMessage()
	if err != nil {
		var closeError *websocket.CloseError
		if errors.As(err, &closeError) {
			t.Fatalf("connection closed before %s: %d %s", expected, closeError.Code, closeError.Text)
		}
		t.Fatalf("read %s: %v", expected, err)
	}
	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != expected {
		t.Fatalf("got event %q while waiting for %q: %s", envelope.Type, expected, data)
	}
	var result T
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func writeClient(t *testing.T, connection *websocket.Conn, envelope ClientEnvelope) {
	t.Helper()
	if err := connection.WriteJSON(envelope); err != nil {
		t.Fatal(err)
	}
}

func expectCloseCode(t *testing.T, connection *websocket.Conn, expected int) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := connection.ReadMessage()
	var closeError *websocket.CloseError
	if !errors.As(err, &closeError) || closeError.Code != expected {
		t.Fatalf("close error = %v, want code %d", err, expected)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
