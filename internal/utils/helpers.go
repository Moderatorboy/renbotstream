package utils

import (
	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/cache"
	"EverythingSuckz/fsb/internal/types"
	"context"
	"errors"
	"fmt"
	"math/rand"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// Helper to convert -100xxxx to xxxx
func GetNormalizedChatID(id int64) int64 {
	// Agar ID -100 se shuru hoti hai (13 digits negative)
	if id < -1000000000000 {
		return (id * -1) - 1000000000000
	}
	// Agar already raw ID hai ya group ID hai
	return id
}

// https://stackoverflow.com/a/70802740/15807350
func Contains[T comparable](s []T, e T) bool {
	for _, v := range s {
		if v == e {
			return true
		}
	}
	return false
}

func GetTGMessage(ctx context.Context, client *gotgproto.Client, channelID int64, messageID int) (*tg.Message, error) {
	inputMessageID := tg.InputMessageClass(&tg.InputMessageID{ID: messageID})
	
	// ✅ Fix: Use Normalized ID
	channel, err := GetChannelPeer(ctx, client.API(), client.PeerStorage, channelID)
	if err != nil {
		// Attempt to refresh dialogs to find the channel
		Logger.Info("Channel not found in cache, refreshing dialogs...", zap.Int64("channelID", channelID))
		_, _ = client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: 0,
			OffsetID:   0,
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      100,
			Hash:       0,
		})
		// Retry getting the peer
		channel, err = GetChannelPeer(ctx, client.API(), client.PeerStorage, channelID)
		if err != nil {
			return nil, err
		}
	}
	
	messageRequest := tg.ChannelsGetMessagesRequest{Channel: channel, ID: []tg.InputMessageClass{inputMessageID}}
	res, err := client.API().ChannelsGetMessages(ctx, &messageRequest)
	if err != nil {
		return nil, err
	}
	messages := res.(*tg.MessagesChannelMessages)
	
	if len(messages.Messages) == 0 {
		return nil, fmt.Errorf("message not found or deleted")
	}
	message := messages.Messages[0]
	if _, ok := message.(*tg.Message); ok {
		return message.(*tg.Message), nil
	} else {
		return nil, fmt.Errorf("this file was deleted")
	}
}

func FileFromMedia(media tg.MessageMediaClass) (*types.File, error) {
	switch media := media.(type) {
	case *tg.MessageMediaDocument:
		document, ok := media.Document.AsNotEmpty()
		if !ok {
			return nil, fmt.Errorf("unexpected type %T", media)
		}
		var fileName string
		for _, attribute := range document.Attributes {
			if name, ok := attribute.(*tg.DocumentAttributeFilename); ok {
				fileName = name.FileName
				break
			}
		}
		return &types.File{
			Location: document.AsInputDocumentFileLocation(),
			FileSize: document.Size,
			FileName: fileName,
			MimeType: document.MimeType,
			ID:       document.ID,
		}, nil
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.AsNotEmpty()
		if !ok {
			return nil, fmt.Errorf("unexpected type %T", media)
		}
		sizes := photo.Sizes
		if len(sizes) == 0 {
			return nil, errors.New("photo has no sizes")
		}
		photoSize := sizes[len(sizes)-1]
		size, ok := photoSize.AsNotEmpty()
		if !ok {
			return nil, errors.New("photo size is empty")
		}
		location := new(tg.InputPhotoFileLocation)
		location.ID = photo.GetID()
		location.AccessHash = photo.GetAccessHash()
		location.FileReference = photo.GetFileReference()
		location.ThumbSize = size.GetType()
		return &types.File{
			Location: location,
			FileSize: 0, 
			FileName: fmt.Sprintf("photo_%d.jpg", photo.GetID()),
			MimeType: "image/jpeg",
			ID:       photo.GetID(),
		}, nil
	}
	return nil, fmt.Errorf("unexpected type %T", media)
}

func FileFromMessage(ctx context.Context, client *gotgproto.Client, channelID int64, messageID int) (*types.File, error) {
	// ✅ Fix: Use Normalized ID for Cache Key
	normID := GetNormalizedChatID(channelID)
	key := fmt.Sprintf("file:%d:%d:%d", normID, messageID, client.Self.ID)
	
	log := Logger.Named("GetMessageMedia")
	var cachedMedia types.File
	err := cache.GetCache().Get(key, &cachedMedia)
	if err == nil {
		log.Debug("Using cached media message properties", zap.Int("messageID", messageID), zap.Int64("channelID", channelID))
		return &cachedMedia, nil
	}
	
	log.Debug("Fetching file properties from message ID", zap.Int("messageID", messageID), zap.Int64("channelID", channelID))
	
	message, err := GetTGMessage(ctx, client, channelID, messageID)
	if err != nil {
		return nil, err
	}
	file, err := FileFromMedia(message.Media)
	if err != nil {
		return nil, err
	}
	err = cache.GetCache().Set(
		key,
		file,
		3600,
	)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func GetChannelPeer(ctx context.Context, api *tg.Client, peerStorage *storage.PeerStorage, targetChannelID int64) (*tg.InputChannel, error) {
	// ✅ Fix: Convert -100xxxx to xxxx before searching
	normID := GetNormalizedChatID(targetChannelID)
	
	cachedInputPeer := peerStorage.GetInputPeerById(normID)

	switch peer := cachedInputPeer.(type) {
	case *tg.InputPeerEmpty:
		break
	case *tg.InputPeerChannel:
		return &tg.InputChannel{
			ChannelID:  peer.ChannelID,
			AccessHash: peer.AccessHash,
		}, nil
	}
	
	inputChannel := &tg.InputChannel{
		ChannelID: normID, // Use Normalized ID
	}
	channels, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})
	if err != nil {
		return nil, err
	}
	if len(channels.GetChats()) == 0 {
		return nil, errors.New("no channels found")
	}
	channel, ok := channels.GetChats()[0].(*tg.Channel)
	if !ok {
		return nil, errors.New("type assertion to *tg.Channel failed")
	}
	
	peerStorage.AddPeer(channel.GetID(), channel.AccessHash, storage.TypeChannel, "")
	return channel.AsInput(), nil
}

func ForwardMessages(ctx *ext.Context, fromChatId, toChatId int64, messageID int) (*tg.Updates, error) {
	// ✅ Fix: Normalize IDs for Forwarding too
	normFromID := GetNormalizedChatID(fromChatId)
	normToID := GetNormalizedChatID(toChatId)

	fromPeer := ctx.PeerStorage.GetInputPeerById(normFromID)
	if fromPeer.Zero() {
		return nil, fmt.Errorf("fromChatId: %d is not a valid peer", fromChatId)
	}
	
	toPeer, err := GetChannelPeer(ctx.Context, ctx.Raw, ctx.PeerStorage, normToID)
	if err != nil {
		// Try refreshing dialogs
		_, _ = ctx.Raw.MessagesGetDialogs(ctx.Context, &tg.MessagesGetDialogsRequest{
			Limit: 100,
		})
		// Retry
		toPeer, err = GetChannelPeer(ctx.Context, ctx.Raw, ctx.PeerStorage, normToID)
		if err != nil {
			return nil, err
		}
	}
	
	update, err := ctx.Raw.MessagesForwardMessages(ctx.Context, &tg.MessagesForwardMessagesRequest{
		RandomID: []int64{rand.Int63()},
		FromPeer: fromPeer,
		ID:       []int{messageID},
		ToPeer:   &tg.InputPeerChannel{ChannelID: toPeer.ChannelID, AccessHash: toPeer.AccessHash},
	})
	if err != nil {
		return nil, err
	}
	return update.(*tg.Updates), nil
}
