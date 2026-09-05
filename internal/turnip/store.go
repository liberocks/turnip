package turnip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	eventsChannel = "turnip:v1:events"
	roomsKey      = "turnip:v1:rooms"
	presenceTTL   = 90 * time.Second
	keyTTL        = 3 * time.Minute
)

var (
	ErrRoomFull       = errors.New("room is full")
	ErrNotParticipant = errors.New("participant not found")
	ErrNotOwner       = errors.New("only the room owner may disband it")
)

type Presence struct {
	ID         string `json:"id"`
	Subject    string `json:"-"`
	Name       string `json:"name,omitempty"`
	InstanceID string `json:"-"`
	Owner      bool   `json:"owner"`
}

type DistributedEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	RoomID      string          `json:"room_id"`
	Origin      string          `json:"origin"`
	Participant *Participant    `json:"participant,omitempty"`
	Target      string          `json:"target,omitempty"`
	From        string          `json:"from,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	By          string          `json:"by,omitempty"`
}

type JoinResult struct {
	Owner        bool
	Participants []Participant
}

type RoomStore interface {
	Join(context.Context, Identity, string, string, string) (JoinResult, error)
	Leave(context.Context, string, string, string, string) error
	Refresh(context.Context, string, string) error
	Participant(context.Context, string, string) (Presence, error)
	PublishSignal(context.Context, DistributedEvent) error
	Disband(context.Context, string, string, string, string, string) error
	Subscribe(context.Context) (*redis.PubSub, error)
	Ping(context.Context) error
	Sweep(context.Context, string) error
}

type RedisStore struct {
	client  *redis.Client
	metrics *Metrics
	now     func() time.Time
}

func NewRedisStore(client *redis.Client, metrics *Metrics) *RedisStore {
	return &RedisStore{client: client, metrics: metrics, now: time.Now}
}

func roomMetaKey(roomID string) string           { return "turnip:v1:room:" + roomID + ":meta" }
func roomMembersKey(roomID string) string        { return "turnip:v1:room:" + roomID + ":participants" }
func participantKey(participantID string) string { return "turnip:v1:participant:" + participantID }

var joinScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1])
for _, member in ipairs(expired) do redis.call('DEL', ARGV[11] .. member) end
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[2]) == 0 then redis.call('DEL', KEYS[1]) end
local owner = redis.call('HGET', KEYS[1], 'owner')
if not owner then
  owner = ARGV[2]
  redis.call('HSET', KEYS[1], 'owner', owner, 'created_at', ARGV[1])
end
if redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[7]) then
  return {-1, owner}
end
redis.call('HSET', KEYS[3], 'room_id', ARGV[8], 'subject', ARGV[2], 'name', ARGV[3], 'instance_id', ARGV[4])
redis.call('ZADD', KEYS[2], ARGV[5], ARGV[6])
redis.call('SADD', KEYS[4], ARGV[8])
redis.call('PEXPIRE', KEYS[1], ARGV[9])
redis.call('PEXPIRE', KEYS[2], ARGV[9])
redis.call('PEXPIRE', KEYS[3], ARGV[9])
local event = cjson.decode(ARGV[10])
event.participant.owner = owner == ARGV[2]
redis.call('PUBLISH', KEYS[5], cjson.encode(event))
return {1, owner}
`)

func (s *RedisStore) Join(ctx context.Context, identity Identity, participantID, instanceID, eventID string) (JoinResult, error) {
	now := s.now()
	event := DistributedEvent{
		ID: eventID, Type: "participant_joined", RoomID: identity.RoomID, Origin: instanceID,
		Participant: &Participant{ID: participantID, Name: identity.Name},
	}
	eventJSON, _ := json.Marshal(event)
	keys := []string{roomMetaKey(identity.RoomID), roomMembersKey(identity.RoomID), participantKey(participantID), roomsKey, eventsChannel}
	started := time.Now()
	result, err := joinScript.Run(ctx, s.client, keys,
		now.UnixMilli(), identity.Subject, identity.Name, instanceID,
		now.Add(presenceTTL).UnixMilli(), participantID, MaxRoomConnections, identity.RoomID,
		keyTTL.Milliseconds(), string(eventJSON), "turnip:v1:participant:",
	).Slice()
	s.metrics.ObserveRedis("join", started, err)
	if err != nil {
		return JoinResult{}, err
	}
	status, err := asInt64(result[0])
	if err != nil {
		return JoinResult{}, err
	}
	if status == -1 {
		return JoinResult{}, ErrRoomFull
	}
	owner, _ := result[1].(string)
	participants, err := s.listParticipants(ctx, identity.RoomID, owner)
	if err != nil {
		return JoinResult{}, err
	}
	return JoinResult{Owner: owner == identity.Subject, Participants: participants}, nil
}

var leaveScript = redis.NewScript(`
local score = redis.call('ZSCORE', KEYS[2], ARGV[1])
if not score then return 0 end
if ARGV[6] == 'expired' and tonumber(score) > tonumber(ARGV[7]) then return 0 end
local name = redis.call('HGET', KEYS[3], 'name') or ''
local subject = redis.call('HGET', KEYS[3], 'subject') or ''
local owner = redis.call('HGET', KEYS[1], 'owner') or ''
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('DEL', KEYS[3])
local event = cjson.decode(ARGV[5])
event.participant.name = name
event.participant.owner = owner == subject
redis.call('PUBLISH', KEYS[5], cjson.encode(event))
if redis.call('ZCARD', KEYS[2]) == 0 then
  redis.call('DEL', KEYS[1], KEYS[2])
  redis.call('SREM', KEYS[4], ARGV[2])
else
  redis.call('PEXPIRE', KEYS[1], ARGV[4])
  redis.call('PEXPIRE', KEYS[2], ARGV[4])
end
return 1
`)

func (s *RedisStore) Leave(ctx context.Context, roomID, participantID, instanceID, eventID string) error {
	return s.leave(ctx, roomID, participantID, instanceID, eventID, "normal")
}

func (s *RedisStore) leave(ctx context.Context, roomID, participantID, instanceID, eventID, mode string) error {
	event := DistributedEvent{
		ID: eventID, Type: "participant_left", RoomID: roomID, Origin: instanceID,
		Participant: &Participant{ID: participantID},
	}
	eventJSON, _ := json.Marshal(event)
	keys := []string{roomMetaKey(roomID), roomMembersKey(roomID), participantKey(participantID), roomsKey, eventsChannel}
	started := time.Now()
	err := leaveScript.Run(ctx, s.client, keys, participantID, roomID, instanceID, keyTTL.Milliseconds(), string(eventJSON), mode, s.now().UnixMilli()).Err()
	s.metrics.ObserveRedis("leave", started, err)
	return err
}

var refreshScript = redis.NewScript(`
if not redis.call('ZSCORE', KEYS[2], ARGV[1]) then return 0 end
redis.call('ZADD', KEYS[2], ARGV[2], ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('PEXPIRE', KEYS[2], ARGV[3])
redis.call('PEXPIRE', KEYS[3], ARGV[3])
return 1
`)

func (s *RedisStore) Refresh(ctx context.Context, roomID, participantID string) error {
	keys := []string{roomMetaKey(roomID), roomMembersKey(roomID), participantKey(participantID)}
	started := time.Now()
	result, err := refreshScript.Run(ctx, s.client, keys, participantID, s.now().Add(presenceTTL).UnixMilli(), keyTTL.Milliseconds()).Int64()
	s.metrics.ObserveRedis("refresh", started, err)
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrNotParticipant
	}
	return nil
}

func (s *RedisStore) Participant(ctx context.Context, roomID, participantID string) (Presence, error) {
	started := time.Now()
	pipe := s.client.Pipeline()
	score := pipe.ZScore(ctx, roomMembersKey(roomID), participantID)
	values := pipe.HGetAll(ctx, participantKey(participantID))
	_, err := pipe.Exec(ctx)
	s.metrics.ObserveRedis("participant", started, err)
	if err != nil && !errors.Is(err, redis.Nil) {
		return Presence{}, err
	}
	if score.Val() <= float64(s.now().UnixMilli()) || values.Val()["room_id"] != roomID {
		return Presence{}, ErrNotParticipant
	}
	owner, err := s.client.HGet(ctx, roomMetaKey(roomID), "owner").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Presence{}, ErrNotParticipant
		}
		return Presence{}, err
	}
	return Presence{
		ID: participantID, Subject: values.Val()["subject"], Name: values.Val()["name"],
		InstanceID: values.Val()["instance_id"], Owner: owner == values.Val()["subject"],
	}, nil
}

func (s *RedisStore) listParticipants(ctx context.Context, roomID, owner string) ([]Participant, error) {
	started := time.Now()
	ids, err := s.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: roomMembersKey(roomID), Start: strconv.FormatInt(s.now().UnixMilli()+1, 10), Stop: "+inf", ByScore: true,
	}).Result()
	s.metrics.ObserveRedis("list", started, err)
	if err != nil {
		return nil, err
	}
	participants := make([]Participant, 0, len(ids))
	for _, id := range ids {
		values, getErr := s.client.HGetAll(ctx, participantKey(id)).Result()
		if getErr != nil || values["room_id"] != roomID {
			continue
		}
		participants = append(participants, Participant{ID: id, Name: values["name"], Owner: values["subject"] == owner})
	}
	return participants, nil
}

func (s *RedisStore) PublishSignal(ctx context.Context, event DistributedEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	started := time.Now()
	err = s.client.Publish(ctx, eventsChannel, data).Err()
	s.metrics.ObserveRedis("publish", started, err)
	return err
}

var disbandScript = redis.NewScript(`
local owner = redis.call('HGET', KEYS[1], 'owner')
if not owner then return -2 end
if owner ~= ARGV[1] then return -1 end
local score = redis.call('ZSCORE', KEYS[2], ARGV[3])
if not score or tonumber(score) <= tonumber(ARGV[6]) then return -2 end
local members = redis.call('ZRANGE', KEYS[2], 0, -1)
for _, member in ipairs(members) do redis.call('DEL', ARGV[5] .. member) end
redis.call('DEL', KEYS[1], KEYS[2])
redis.call('SREM', KEYS[3], ARGV[2])
redis.call('PUBLISH', KEYS[4], ARGV[4])
return #members
`)

func (s *RedisStore) Disband(ctx context.Context, roomID, subject, participantID, instanceID, eventID string) error {
	event := DistributedEvent{ID: eventID, Type: "room_disbanded", RoomID: roomID, Origin: instanceID, Reason: "owner_requested", By: participantID}
	data, _ := json.Marshal(event)
	keys := []string{roomMetaKey(roomID), roomMembersKey(roomID), roomsKey, eventsChannel}
	started := time.Now()
	result, err := disbandScript.Run(ctx, s.client, keys, subject, roomID, participantID, string(data), "turnip:v1:participant:", s.now().UnixMilli()).Int64()
	s.metrics.ObserveRedis("disband", started, err)
	if err != nil {
		return err
	}
	if result == -1 {
		return ErrNotOwner
	}
	if result == -2 {
		return ErrNotParticipant
	}
	return nil
}

func (s *RedisStore) Subscribe(ctx context.Context) (*redis.PubSub, error) {
	subscription := s.client.Subscribe(ctx, eventsChannel)
	if _, err := subscription.Receive(ctx); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	return subscription, nil
}

func (s *RedisStore) Ping(ctx context.Context) error {
	started := time.Now()
	err := s.client.Ping(ctx).Err()
	s.metrics.ObserveRedis("ping", started, err)
	return err
}

func (s *RedisStore) Sweep(ctx context.Context, instanceID string) error {
	rooms, err := s.client.SMembers(ctx, roomsKey).Result()
	if err != nil {
		return err
	}
	for _, roomID := range rooms {
		expired, rangeErr := s.client.ZRangeArgs(ctx, redis.ZRangeArgs{
			Key: roomMembersKey(roomID), Start: "-inf", Stop: strconv.FormatInt(s.now().UnixMilli(), 10), ByScore: true,
		}).Result()
		if rangeErr != nil {
			return rangeErr
		}
		for _, participantID := range expired {
			if leaveErr := s.leave(ctx, roomID, participantID, instanceID, randomID(), "expired"); leaveErr != nil {
				return leaveErr
			}
		}
		if count, countErr := s.client.ZCard(ctx, roomMembersKey(roomID)).Result(); countErr != nil {
			return countErr
		} else if count == 0 {
			pipe := s.client.Pipeline()
			pipe.Del(ctx, roomMetaKey(roomID), roomMembersKey(roomID))
			pipe.SRem(ctx, roomsKey, roomID)
			if _, cleanupErr := pipe.Exec(ctx); cleanupErr != nil {
				return cleanupErr
			}
		}
	}
	return nil
}

func asInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}
