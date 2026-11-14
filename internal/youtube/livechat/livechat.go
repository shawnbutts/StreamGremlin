// Package livechat is the wire format of YouTube's live chat stream,
// liveChatMessages.streamList over gRPC (M6 slice 2, D-097,
// docs/adr/0047-youtube-chat-stream.md). It reads and writes only the
// fields StreamGremlin uses, by the field numbers in Google's published
// stream_list.proto (package youtube.api.v3), checked 2026-11-14:
// https://developers.google.com/youtube/v3/live/streaming-live-chat.
// The mock YouTube uses it too, to answer the same way.
package livechat

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/mem"
	"google.golang.org/protobuf/encoding/protowire"
)

// Service and Method name Google's streaming RPC.
const (
	Service = "youtube.api.v3.V3DataLiveChatMessageService"
	Method  = "/" + Service + "/StreamList"
	// Host is Google's gRPC endpoint.
	Host = "youtube.googleapis.com:443"
)

// Type is a message's snippet.type (TypeWrapper.Type).
type Type int32

// The types Google documents.
const (
	TypeInvalid                Type = 0
	TypeText                   Type = 1
	TypeTombstone              Type = 2
	TypeFanFunding             Type = 3
	TypeChatEnded              Type = 4
	TypeSponsorOnlyModeStarted Type = 5
	TypeSponsorOnlyModeEnded   Type = 6
	TypeNewSponsor             Type = 7
	TypeUserBanned             Type = 10
	TypeSuperChat              Type = 15
	TypeSuperSticker           Type = 16
	TypeMemberMilestoneChat    Type = 17
	TypeMembershipGifting      Type = 18
	TypeGiftMembershipReceived Type = 19
	TypePoll                   Type = 20
	TypeGift                   Type = 21
)

var typeNames = map[Type]string{
	TypeText: "textMessageEvent", TypeTombstone: "tombstone", TypeFanFunding: "fanFundingEvent",
	TypeChatEnded: "chatEndedEvent", TypeSponsorOnlyModeStarted: "sponsorOnlyModeStartedEvent",
	TypeSponsorOnlyModeEnded: "sponsorOnlyModeEndedEvent", TypeNewSponsor: "newSponsorEvent",
	TypeUserBanned: "userBannedEvent", TypeSuperChat: "superChatEvent", TypeSuperSticker: "superStickerEvent",
	TypeMemberMilestoneChat: "memberMilestoneChatEvent", TypeMembershipGifting: "membershipGiftingEvent",
	TypeGiftMembershipReceived: "giftMembershipReceivedEvent", TypePoll: "pollEvent", TypeGift: "giftEvent",
}

// String is the type's name in YouTube's JSON, such as textMessageEvent.
func (t Type) String() string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("type%d", int32(t))
}

// Request is LiveChatMessageListRequest.
type Request struct {
	LiveChatID       string   // 1
	Hl               string   // 2
	ProfileImageSize uint32   // 3
	PageToken        string   // 99
	Part             []string // 100
}

// Response is LiveChatMessageListResponse.
type Response struct {
	OfflineAt     string    // 2, set once the stream is offline
	NextPageToken string    // 100602, to resume from
	Items         []Message // 1007
}

// Message is LiveChatMessage.
type Message struct {
	ID      string  // 101
	Snippet Snippet // 2
	Author  Author  // 3
}

// Snippet is LiveChatMessageSnippet.
type Snippet struct {
	Type              Type   // 1
	PublishedAt       string // 4
	DisplayMessage    string // 16
	HasDisplayContent bool   // 17
	Text              string // 19: text_message_details.message_text (1)
	LiveChatID        string // 201
	AuthorChannelID   string // 301
}

// Author is LiveChatMessageAuthorDetails.
type Author struct {
	IsVerified      bool   // 4
	IsChatOwner     bool   // 5
	IsChatSponsor   bool   // 6
	IsChatModerator bool   // 7
	ChannelURL      string // 102
	DisplayName     string // 103
	ProfileImageURL string // 104
	ChannelID       string // 10101
}

// Field numbers.
const (
	reqLiveChatID       = 1
	reqHl               = 2
	reqProfileImageSize = 3
	reqPageToken        = 99
	reqPart             = 100

	respOfflineAt     = 2
	respNextPageToken = 100602
	respItems         = 1007

	msgID      = 101
	msgSnippet = 2
	msgAuthor  = 3

	snType           = 1
	snPublishedAt    = 4
	snDisplayMessage = 16
	snHasDisplay     = 17
	snTextDetails    = 19
	snLiveChatID     = 201
	snAuthorChannel  = 301
	textMessageText  = 1

	auVerified   = 4
	auOwner      = 5
	auSponsor    = 6
	auModerator  = 7
	auChannelURL = 102
	auName       = 103
	auImage      = 104
	auChannelID  = 10101
)

// ErrMalformed means bytes that aren't the message expected.
var ErrMalformed = errors.New("livechat: a malformed message")

func appendString(b []byte, n protowire.Number, s string) []byte {
	if s == "" {
		return b
	}
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendString(b, s)
}

func appendBool(b []byte, n protowire.Number, v bool) []byte {
	if !v {
		return b
	}
	b = protowire.AppendTag(b, n, protowire.VarintType)
	return protowire.AppendVarint(b, 1)
}

func appendMessage(b []byte, n protowire.Number, m []byte) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendBytes(b, m)
}

// Marshal encodes the request.
func (r Request) Marshal() []byte {
	var b []byte
	b = appendString(b, reqLiveChatID, r.LiveChatID)
	b = appendString(b, reqHl, r.Hl)
	if r.ProfileImageSize != 0 {
		b = protowire.AppendTag(b, reqProfileImageSize, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(r.ProfileImageSize))
	}
	b = appendString(b, reqPageToken, r.PageToken)
	for _, p := range r.Part {
		b = protowire.AppendTag(b, reqPart, protowire.BytesType)
		b = protowire.AppendString(b, p)
	}
	return b
}

// Marshal encodes the response (for the mock YouTube).
func (r Response) Marshal() []byte {
	var b []byte
	b = appendString(b, respOfflineAt, r.OfflineAt)
	for _, m := range r.Items {
		b = appendMessage(b, respItems, m.Marshal())
	}
	b = appendString(b, respNextPageToken, r.NextPageToken)
	return b
}

// Marshal encodes one message.
func (m Message) Marshal() []byte {
	var sn []byte
	if m.Snippet.Type != 0 {
		sn = protowire.AppendTag(sn, snType, protowire.VarintType)
		sn = protowire.AppendVarint(sn, uint64(int64(m.Snippet.Type))) //nolint:gosec // protobuf encodes an enum as its int64 bits
	}
	sn = appendString(sn, snPublishedAt, m.Snippet.PublishedAt)
	sn = appendString(sn, snDisplayMessage, m.Snippet.DisplayMessage)
	sn = appendBool(sn, snHasDisplay, m.Snippet.HasDisplayContent)
	if m.Snippet.Type == TypeText {
		sn = appendMessage(sn, snTextDetails, appendString(nil, textMessageText, m.Snippet.Text))
	}
	sn = appendString(sn, snLiveChatID, m.Snippet.LiveChatID)
	sn = appendString(sn, snAuthorChannel, m.Snippet.AuthorChannelID)

	var au []byte
	au = appendBool(au, auVerified, m.Author.IsVerified)
	au = appendBool(au, auOwner, m.Author.IsChatOwner)
	au = appendBool(au, auSponsor, m.Author.IsChatSponsor)
	au = appendBool(au, auModerator, m.Author.IsChatModerator)
	au = appendString(au, auChannelURL, m.Author.ChannelURL)
	au = appendString(au, auName, m.Author.DisplayName)
	au = appendString(au, auImage, m.Author.ProfileImageURL)
	au = appendString(au, auChannelID, m.Author.ChannelID)

	var b []byte
	b = appendString(b, msgID, m.ID)
	b = appendMessage(b, msgSnippet, sn)
	b = appendMessage(b, msgAuthor, au)
	return b
}

// fields walks b's fields, calling f with each one's number, wire type and
// value (a varint, or bytes). Unknown fields are skipped, as protobuf does.
func fields(b []byte, f func(n protowire.Number, t protowire.Type, v uint64, bs []byte) error) error {
	for len(b) > 0 {
		n, t, l := protowire.ConsumeTag(b)
		if l < 0 {
			return ErrMalformed
		}
		b = b[l:]
		var v uint64
		var bs []byte
		switch t {
		case protowire.VarintType:
			v, l = protowire.ConsumeVarint(b)
		case protowire.BytesType:
			bs, l = protowire.ConsumeBytes(b)
		default:
			l = protowire.ConsumeFieldValue(n, t, b)
		}
		if l < 0 {
			return ErrMalformed
		}
		b = b[l:]
		if err := f(n, t, v, bs); err != nil {
			return err
		}
	}
	return nil
}

// ParseRequest decodes a request (for the mock YouTube).
func ParseRequest(b []byte) (Request, error) {
	var r Request
	err := fields(b, func(n protowire.Number, t protowire.Type, v uint64, bs []byte) error {
		switch {
		case n == reqLiveChatID && t == protowire.BytesType:
			r.LiveChatID = string(bs)
		case n == reqHl && t == protowire.BytesType:
			r.Hl = string(bs)
		case n == reqProfileImageSize && t == protowire.VarintType:
			r.ProfileImageSize = uint32(v) //nolint:gosec // a proto uint32
		case n == reqPageToken && t == protowire.BytesType:
			r.PageToken = string(bs)
		case n == reqPart && t == protowire.BytesType:
			r.Part = append(r.Part, string(bs))
		}
		return nil
	})
	return r, err
}

// ParseResponse decodes a response.
func ParseResponse(b []byte) (Response, error) {
	var r Response
	err := fields(b, func(n protowire.Number, t protowire.Type, _ uint64, bs []byte) error {
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case respOfflineAt:
			r.OfflineAt = string(bs)
		case respNextPageToken:
			r.NextPageToken = string(bs)
		case respItems:
			m, err := parseMessage(bs)
			if err != nil {
				return err
			}
			r.Items = append(r.Items, m)
		}
		return nil
	})
	return r, err
}

func parseMessage(b []byte) (Message, error) {
	var m Message
	err := fields(b, func(n protowire.Number, t protowire.Type, _ uint64, bs []byte) error {
		if t != protowire.BytesType {
			return nil
		}
		var err error
		switch n {
		case msgID:
			m.ID = string(bs)
		case msgSnippet:
			m.Snippet, err = parseSnippet(bs)
		case msgAuthor:
			m.Author, err = parseAuthor(bs)
		}
		return err
	})
	return m, err
}

func parseSnippet(b []byte) (Snippet, error) {
	var s Snippet
	err := fields(b, func(n protowire.Number, t protowire.Type, v uint64, bs []byte) error {
		if t == protowire.VarintType {
			switch n {
			case snType:
				s.Type = Type(int32(v)) //nolint:gosec // a proto enum
			case snHasDisplay:
				s.HasDisplayContent = v != 0
			}
			return nil
		}
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case snPublishedAt:
			s.PublishedAt = string(bs)
		case snDisplayMessage:
			s.DisplayMessage = string(bs)
		case snLiveChatID:
			s.LiveChatID = string(bs)
		case snAuthorChannel:
			s.AuthorChannelID = string(bs)
		case snTextDetails:
			return fields(bs, func(n protowire.Number, t protowire.Type, _ uint64, bs []byte) error {
				if n == textMessageText && t == protowire.BytesType {
					s.Text = string(bs)
				}
				return nil
			})
		}
		return nil
	})
	return s, err
}

func parseAuthor(b []byte) (Author, error) {
	var a Author
	err := fields(b, func(n protowire.Number, t protowire.Type, v uint64, bs []byte) error {
		if t == protowire.VarintType {
			switch n {
			case auVerified:
				a.IsVerified = v != 0
			case auOwner:
				a.IsChatOwner = v != 0
			case auSponsor:
				a.IsChatSponsor = v != 0
			case auModerator:
				a.IsChatModerator = v != 0
			}
			return nil
		}
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case auChannelURL:
			a.ChannelURL = string(bs)
		case auName:
			a.DisplayName = string(bs)
		case auImage:
			a.ProfileImageURL = string(bs)
		case auChannelID:
			a.ChannelID = string(bs)
		}
		return nil
	})
	return a, err
}

// Raw is an encoded message, for Codec.
type Raw struct{ B []byte }

// Codec passes Raw messages to and from gRPC as they are, so no generated
// code is needed. Its name is "proto": the bytes are protobuf, and the
// content type is application/grpc+proto.
type Codec struct{}

var _ encoding.CodecV2 = Codec{}

// Marshal implements encoding.CodecV2.
func (Codec) Marshal(v any) (mem.BufferSlice, error) {
	r, ok := v.(*Raw)
	if !ok {
		return nil, fmt.Errorf("livechat: can't encode %T", v)
	}
	return mem.BufferSlice{mem.SliceBuffer(r.B)}, nil
}

// Unmarshal implements encoding.CodecV2.
func (Codec) Unmarshal(data mem.BufferSlice, v any) error {
	r, ok := v.(*Raw)
	if !ok {
		return fmt.Errorf("livechat: can't decode into %T", v)
	}
	r.B = data.Materialize()
	return nil
}

// Name implements encoding.CodecV2.
func (Codec) Name() string { return "proto" }
