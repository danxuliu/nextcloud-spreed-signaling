/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
 *
 * @author Joachim Bauch <bauch@struktur.de>
 *
 * @license GNU AGPL version 3 or any later version
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */
package signaling

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_FederationInvalidToken(t *testing.T) {
	CatchLogForTest(t)

	assert := assert.New(t)
	require := require.New(t)

	_, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client := NewTestClient(t, server2, hub2)
	defer client.CloseWithBye()
	require.NoError(client.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	_, err := client.RunUntilHello(ctx)
	require.NoError(err)

	msg := &ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    "test-room",
			SessionId: "room-session-id",
			Federation: &RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				Token:        "invalid-token",
			},
		},
	}
	require.NoError(client.WriteJSON(msg))

	if message, err := client.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal(msg.Id, message.Id)
		require.Equal("error", message.Type)
		require.Equal("invalid_token", message.Error.Code)
	}
}

func Test_Federation(t *testing.T) {
	CatchLogForTest(t)

	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	roomId := "test-room"
	federatedRoomId := roomId + "@federated"
	room1, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, room1.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	now := time.Now()
	token, err := client1.CreateHelloV2Token(testDefaultUserId+"2", now, now.Add(time.Minute))
	require.NoError(err)

	msg := &ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    federatedRoomId,
			SessionId: federatedRoomId + "-" + hello2.Hello.SessionId,
			Federation: &RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				RoomId:       roomId,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId string
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"2", evt.UserId)
	}

	// The client2 will see its own session id, not the one from the remote server.
	assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

	// Leaving and re-joining a room as "direct" session will trigger correct events.
	if room, err := client1.JoinRoom(ctx, ""); assert.NoError(err) {
		assert.Equal("", room.Room.RoomId)
	}

	assert.NoError(client2.RunUntilLeft(ctx, hello1.Hello))

	if room, err := client1.JoinRoom(ctx, roomId); assert.NoError(err) {
		assert.Equal(roomId, room.Room.RoomId)
	}

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello, &HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	}))
	assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello))

	// Leaving and re-joining a room as "federated" session will trigger correct events.
	if room, err := client2.JoinRoom(ctx, ""); assert.NoError(err) {
		assert.Equal("", room.Room.RoomId)
	}

	assert.NoError(client1.RunUntilLeft(ctx, &HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	}))

	require.NoError(client2.WriteJSON(msg))
	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(federatedRoomId, message.Room.RoomId)
	}

	// Client1 will receive the updated "remoteSessionId"
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"2", evt.UserId)
	}
	assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

	// Test sending messages between sessions.
	data1 := "from-1-to-2"
	data2 := "from-2-to-1"
	if assert.NoError(client1.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, data1)) {
		var payload string
		if assert.NoError(checkReceiveClientMessage(ctx, client2, "session", hello1.Hello, &payload)) {
			assert.Equal(data1, payload)
		}
	}

	if assert.NoError(client1.SendControl(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, data1)) {
		var payload string
		if assert.NoError(checkReceiveClientControl(ctx, client2, "session", hello1.Hello, &payload)) {
			assert.Equal(data1, payload)
		}
	}

	if assert.NoError(client2.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, data2)) {
		var payload string
		if assert.NoError(checkReceiveClientMessage(ctx, client1, "session", &HelloServerMessage{
			SessionId: remoteSessionId,
			UserId:    testDefaultUserId + "2",
		}, &payload)) {
			assert.Equal(data2, payload)
		}
	}

	if assert.NoError(client2.SendControl(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello1.Hello.SessionId,
	}, data2)) {
		var payload string
		if assert.NoError(checkReceiveClientControl(ctx, client1, "session", &HelloServerMessage{
			SessionId: remoteSessionId,
			UserId:    testDefaultUserId + "2",
		}, &payload)) {
			assert.Equal(data2, payload)
		}
	}

	data3 := "from-2-to-2"
	// Clients can't send to their own (local) session id.
	if assert.NoError(client2.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, data3)) {
		ctx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel2()

		if message, err := client2.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
			t.Error(err)
		} else {
			assert.Nil(message)
		}
	}

	// Clients can't send to their own (remote) session id.
	if assert.NoError(client2.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: remoteSessionId,
	}, data3)) {
		ctx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel2()

		if message, err := client2.RunUntilMessage(ctx2); err != nil && err != ErrNoMessageReceived && err != context.DeadlineExceeded {
			t.Error(err)
		} else {
			assert.Nil(message)
		}
	}

	// Simulate request from the backend that somebody joined the call.
	users := []map[string]interface{}{
		{
			"sessionId": remoteSessionId,
			"inCall":    1,
		},
	}
	room := hub1.getRoom(roomId)
	require.NotNil(room)
	room.PublishUsersInCallChanged(users, users)
	var event *EventServerMessage
	assert.NoError(checkReceiveClientEvent(ctx, client1, "update", &event))
	assert.Equal(remoteSessionId, event.Update.Users[0]["sessionId"])
	assert.Equal(roomId, event.Update.RoomId)

	assert.NoError(checkReceiveClientEvent(ctx, client2, "update", &event))
	assert.Equal(hello2.Hello.SessionId, event.Update.Users[0]["sessionId"])
	assert.Equal(federatedRoomId, event.Update.RoomId)

	// Joining another "direct" session will trigger correct events.

	client3 := NewTestClient(t, server1, hub1)
	defer client3.CloseWithBye()
	require.NoError(client3.SendHelloV2(testDefaultUserId + "3"))

	hello3, err := client3.RunUntilHello(ctx)
	require.NoError(err)

	if room, err := client3.JoinRoom(ctx, roomId); assert.NoError(err) {
		require.Equal(roomId, room.Room.RoomId)
	}

	assert.NoError(client1.RunUntilJoined(ctx, hello3.Hello))
	assert.NoError(client2.RunUntilJoined(ctx, hello3.Hello))

	assert.NoError(client3.RunUntilJoined(ctx, hello1.Hello, &HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	}, hello3.Hello))

	// Joining another "federated" session will trigger correct events.

	client4 := NewTestClient(t, server2, hub1)
	defer client4.CloseWithBye()
	require.NoError(client4.SendHelloV2(testDefaultUserId + "4"))

	hello4, err := client4.RunUntilHello(ctx)
	require.NoError(err)

	token, err = client4.CreateHelloV2Token(testDefaultUserId+"4", now, now.Add(time.Minute))
	require.NoError(err)

	msg = &ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomId + "-" + hello4.Hello.SessionId,
			Federation: &RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				Token:        token,
			},
		},
	}
	require.NoError(client4.WriteJSON(msg))

	if message, err := client4.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(roomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId4 string
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId4 = evt.SessionId
		assert.NotEqual(hello4.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"4", evt.UserId)
	}

	assert.NoError(client2.RunUntilJoined(ctx, &HelloServerMessage{
		SessionId: remoteSessionId4,
		UserId:    hello4.Hello.UserId,
	}))

	assert.NoError(client3.RunUntilJoined(ctx, &HelloServerMessage{
		SessionId: remoteSessionId4,
		UserId:    hello4.Hello.UserId,
	}))

	assert.NoError(client4.RunUntilJoined(ctx, hello1.Hello, &HelloServerMessage{
		SessionId: remoteSessionId,
		UserId:    hello2.Hello.UserId,
	}, hello3.Hello, hello4.Hello))

	room3, err := client2.JoinRoom(ctx, "")
	if assert.NoError(err) {
		assert.Equal("", room3.Room.RoomId)
	}
}

func Test_FederationMedia(t *testing.T) {
	CatchLogForTest(t)

	assert := assert.New(t)
	require := require.New(t)

	hub1, hub2, server1, server2 := CreateClusteredHubsForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mcu1, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu1.Start(ctx))
	defer mcu1.Stop()

	hub1.SetMcu(mcu1)

	mcu2, err := NewTestMCU()
	require.NoError(err)
	require.NoError(mcu2.Start(ctx))
	defer mcu2.Stop()

	hub2.SetMcu(mcu2)

	client1 := NewTestClient(t, server1, hub1)
	defer client1.CloseWithBye()
	require.NoError(client1.SendHelloV2(testDefaultUserId + "1"))

	client2 := NewTestClient(t, server2, hub2)
	defer client2.CloseWithBye()
	require.NoError(client2.SendHelloV2(testDefaultUserId + "2"))

	hello1, err := client1.RunUntilHello(ctx)
	require.NoError(err)

	hello2, err := client2.RunUntilHello(ctx)
	require.NoError(err)

	roomId := "test-room"
	room1, err := client1.JoinRoom(ctx, roomId)
	require.NoError(err)
	require.Equal(roomId, room1.Room.RoomId)

	assert.NoError(client1.RunUntilJoined(ctx, hello1.Hello))

	now := time.Now()
	token, err := client1.CreateHelloV2Token(testDefaultUserId+"2", now, now.Add(time.Minute))
	require.NoError(err)

	msg := &ClientMessage{
		Id:   "join-room-fed",
		Type: "room",
		Room: &RoomClientMessage{
			RoomId:    roomId,
			SessionId: roomId + "-" + hello2.Hello.SessionId,
			Federation: &RoomFederationMessage{
				SignalingUrl: server1.URL,
				NextcloudUrl: server1.URL,
				Token:        token,
			},
		},
	}
	require.NoError(client2.WriteJSON(msg))

	if message, err := client2.RunUntilMessage(ctx); assert.NoError(err) {
		assert.Equal(msg.Id, message.Id)
		require.Equal("room", message.Type)
		require.Equal(roomId, message.Room.RoomId)
	}

	// The client1 will see the remote session id for client2.
	var remoteSessionId string
	if message, err := client1.RunUntilMessage(ctx); assert.NoError(err) {
		client1.checkSingleMessageJoined(message)
		evt := message.Event.Join[0]
		remoteSessionId = evt.SessionId
		assert.NotEqual(hello2.Hello.SessionId, remoteSessionId)
		assert.Equal(testDefaultUserId+"2", evt.UserId)
	}

	// The client2 will see its own session id, not the one from the remote server.
	assert.NoError(client2.RunUntilJoined(ctx, hello1.Hello, hello2.Hello))

	require.NoError(client2.SendMessage(MessageClientMessageRecipient{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
	}, MessageClientMessageData{
		Type:     "offer",
		Sid:      "12345",
		RoomType: "screen",
		Payload: map[string]interface{}{
			"sdp": MockSdpOfferAudioAndVideo,
		},
	}))

	require.NoError(client2.RunUntilAnswerFromSender(ctx, MockSdpAnswerAudioAndVideo, &MessageServerMessageSender{
		Type:      "session",
		SessionId: hello2.Hello.SessionId,
		UserId:    hello2.Hello.UserId,
	}))
}
