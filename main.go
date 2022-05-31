package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
)

type status struct {
	startTime     int64
	questionIndex int
	timer         *time.Timer
}

type Bot struct {
	config    *Config
	b         *gotgbot.Bot
	logger    *log.Logger
	statusMap map[int64]*status
}

func NewBot(config *Config) (*Bot, error) {
	b, err := gotgbot.NewBot(config.BotToken, nil)
	if err != nil {
		return nil, err
	}

	logger := log.Default()
	logger.SetPrefix("[gotgbot] ")

	bot := &Bot{
		config:    config,
		b:         b,
		logger:    logger,
		statusMap: make(map[int64]*status),
	}

	return bot, nil
}

func main() {
	rand.Seed(time.Now().UnixNano())

	config, err := parseConfig()
	if err != nil {
		panic(err)
	}

	if len(config.Questions) == 0 {
		panic(fmt.Errorf("no questions found"))
	}

	bot, err := NewBot(config)
	if err != nil {
		panic(err)
	}

	bot.logger.Println(config)

	updater := ext.NewUpdater(&ext.UpdaterOpts{
		ErrorLog: bot.logger,
		DispatcherOpts: ext.DispatcherOpts{
			Error: func(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
				bot.logger.Println("Error while handling update:", err)
				return ext.DispatcherActionNoop
			},
			Panic: func(b *gotgbot.Bot, ctx *ext.Context, r interface{}) {
				bot.logger.Println("Panic while handling update:", r)
			},
			ErrorLog: bot.logger,
		},
	})
	dispatcher := updater.Dispatcher

	dispatcher.AddHandler(handlers.NewChatJoinRequest(func(cjr *gotgbot.ChatJoinRequest) bool {
		return cjr.Chat.Id == bot.config.ChatID
	}, bot.handleNewChatJoinRequest))
	dispatcher.AddHandler(handlers.NewCallback(bot.filterCallbackQuery, bot.handleCallbackQuery))

	err = updater.StartPolling(bot.b, &ext.PollingOpts{
		DropPendingUpdates: false,
		GetUpdatesOpts: gotgbot.GetUpdatesOpts{
			AllowedUpdates: []string{"callback_query", "chat_join_request"},
		},
	})
	if err != nil {
		bot.logger.Panic("failed to start polling: " + err.Error())
	}
	bot.logger.Printf("%s has been started...\n", bot.b.User.Username)

	updater.Idle()
}

func sha256sum(text string) string {
	h := sha256.New()
	h.Write([]byte(text))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (bot *Bot) handleNewChatJoinRequest(b *gotgbot.Bot, ctx *ext.Context) error {
	bot.logger.Println(ctx.EffectiveUser.FirstName, ctx.EffectiveUser.LastName, ctx.EffectiveUser.Id, ctx.EffectiveChat.Id)

	question := bot.config.Questions[0]

	choices := append([]string{question.Answer}, question.Choices...)
	rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })

	startTime := time.Now().Unix()

	var inlineKeyboardButtonsArr [][]gotgbot.InlineKeyboardButton
	for _, choice := range choices {
		inlineKeyboardButtonsArr = append(inlineKeyboardButtonsArr, []gotgbot.InlineKeyboardButton{{
			Text:         choice,
			CallbackData: sha256sum(fmt.Sprintf("%s%d", choice, startTime)),
		}})
	}

	msg, err := b.SendMessage(ctx.EffectiveUser.Id, fmt.Sprintf("%s\n\n%02d. %s", bot.config.Messages.AskQuestion, 1, question.Question), &gotgbot.SendMessageOpts{
		ProtectContent: true,
		ReplyMarkup: gotgbot.InlineKeyboardMarkup{
			InlineKeyboard: inlineKeyboardButtonsArr,
		},
	})
	if err != nil {
		return err
	}

	timeoutFunc := func(userId, msgId int64) func() {
		return func() {
			bot.logger.Println("timeout for user", userId, "message", msgId)
			if _, ok, err := b.EditMessageText(bot.config.Messages.TimeoutError, &gotgbot.EditMessageTextOpts{
				ChatId:      userId,
				MessageId:   msgId,
				ReplyMarkup: gotgbot.InlineKeyboardMarkup{},
			}); err != nil || !ok {
				bot.logger.Println("failed to edit message:", ok, err)
			}
			bot.deleteStatusAndDecline(userId)
		}
	}

	bot.statusMap[ctx.EffectiveUser.Id] = &status{
		startTime:     startTime,
		questionIndex: 0,
		timer:         time.AfterFunc(time.Duration(bot.config.Timeout)*time.Second, timeoutFunc(ctx.EffectiveUser.Id, msg.MessageId)),
	}

	return nil
}

func (bot *Bot) handleCallbackQuery(b *gotgbot.Bot, ctx *ext.Context) error {
	status, ok := bot.statusMap[ctx.EffectiveUser.Id]
	if !ok {
		bot.logger.Println("no status found for user", ctx.EffectiveUser.Id)
		if _, _, err := b.EditMessageText(bot.config.Messages.InvalidButton, &gotgbot.EditMessageTextOpts{
			ChatId:      ctx.EffectiveChat.Id,
			MessageId:   ctx.CallbackQuery.Message.MessageId,
			ReplyMarkup: gotgbot.InlineKeyboardMarkup{},
		}); err != nil {
			return err
		}
		return nil
	}

	answer := sha256sum(fmt.Sprintf("%s%d", bot.config.Questions[status.questionIndex].Answer, status.startTime))
	if answer != ctx.CallbackQuery.Data {
		bot.logger.Println("wrong answer", ctx.EffectiveUser.Id)
		if _, _, err := b.EditMessageText(bot.config.Messages.WrongAnswer, &gotgbot.EditMessageTextOpts{
			ChatId:          ctx.EffectiveChat.Id,
			MessageId:       ctx.CallbackQuery.Message.MessageId,
			InlineMessageId: ctx.CallbackQuery.InlineMessageId,
			ReplyMarkup:     gotgbot.InlineKeyboardMarkup{},
		}); err != nil {
			bot.stopStatusTimer(status)
			bot.deleteStatusAndDecline(ctx.EffectiveUser.Id)
			return err
		}
		bot.stopStatusTimer(status)
		bot.deleteStatusAndDecline(ctx.EffectiveUser.Id)
		return nil
	}

	if status.questionIndex >= len(bot.config.Questions)-1 {
		bot.logger.Println("all questions answered", ctx.EffectiveUser.Id)
		if _, _, err := b.EditMessageText(bot.config.Messages.CorrectAnswer, &gotgbot.EditMessageTextOpts{
			ChatId:          ctx.EffectiveChat.Id,
			MessageId:       ctx.CallbackQuery.Message.MessageId,
			InlineMessageId: ctx.CallbackQuery.InlineMessageId,
			ReplyMarkup:     gotgbot.InlineKeyboardMarkup{},
		}); err != nil {
			bot.stopStatusTimer(status)
			bot.deleteStatusAndDecline(ctx.EffectiveUser.Id)
			return err
		}
		bot.stopStatusTimer(status)
		bot.deleteStatusAndApprove(ctx.EffectiveUser.Id)
		return nil
	}

	status.questionIndex = status.questionIndex + 1
	question := bot.config.Questions[status.questionIndex]

	choices := append([]string{question.Answer}, question.Choices...)
	rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })

	var inlineKeyboardButtonsArr [][]gotgbot.InlineKeyboardButton
	for _, choice := range choices {
		inlineKeyboardButtonsArr = append(inlineKeyboardButtonsArr, []gotgbot.InlineKeyboardButton{{
			Text:         choice,
			CallbackData: sha256sum(fmt.Sprintf("%s%d", choice, status.startTime)),
		}})
	}

	if _, _, err := b.EditMessageText(fmt.Sprintf("%s\n\n%02d. %s", bot.config.Messages.AskQuestion, status.questionIndex+1, question.Question), &gotgbot.EditMessageTextOpts{
		ChatId:          ctx.EffectiveChat.Id,
		MessageId:       ctx.CallbackQuery.Message.MessageId,
		InlineMessageId: ctx.CallbackQuery.InlineMessageId,
		ReplyMarkup: gotgbot.InlineKeyboardMarkup{
			InlineKeyboard: inlineKeyboardButtonsArr,
		},
	}); err != nil {
		bot.stopStatusTimer(status)
		bot.deleteStatusAndDecline(ctx.EffectiveUser.Id)
		return err
	}

	return nil
}

func (bot *Bot) filterCallbackQuery(c *gotgbot.CallbackQuery) bool {
	return c.Data != ""
}

func (bot *Bot) deleteStatusAndApprove(userId int64) {
	bot.logger.Println("Approve", userId)
	if _, ok := bot.statusMap[userId]; ok {
		delete(bot.statusMap, userId)
		if _, err := bot.b.ApproveChatJoinRequest(bot.config.ChatID, userId, &gotgbot.ApproveChatJoinRequestOpts{}); err != nil {
			bot.logger.Println("failed to approve chat join request:", err)
		}
	}
}

func (bot *Bot) deleteStatusAndDecline(userId int64) {
	bot.logger.Println("Decline", userId)
	if _, ok := bot.statusMap[userId]; ok {
		delete(bot.statusMap, userId)
		if _, err := bot.b.DeclineChatJoinRequest(bot.config.ChatID, userId, &gotgbot.DeclineChatJoinRequestOpts{}); err != nil {
			bot.logger.Println("failed to decline chat join request:", err)
		}
		if _, err := bot.b.BanChatMember(bot.config.ChatID, userId, &gotgbot.BanChatMemberOpts{
			UntilDate: time.Now().Unix() + bot.config.BanTime,
		}); err != nil {
			bot.logger.Println("failed to ban user:", err)
		}
	}
}

func (bot *Bot) stopStatusTimer(status *status) {
	if !status.timer.Stop() {
		<-status.timer.C
	}
}
