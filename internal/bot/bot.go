package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"

	"max-echo-bot/internal/config"
	"max-echo-bot/internal/db"
	"max-echo-bot/internal/email"
	"max-echo-bot/internal/updater"
)

type Bot struct {
	api       *maxbot.Api
	cfg       *config.Config
	db        *db.DB
	events    <-chan email.Event
	worker    *email.Worker
	version   string
	isService bool
	stop      context.CancelFunc
	states    *stateStore
}

func NewBot(api *maxbot.Api, cfg *config.Config, database *db.DB, events <-chan email.Event, worker *email.Worker, version string, isService bool) *Bot {
	return &Bot{api: api, cfg: cfg, db: database, events: events, worker: worker, version: version, isService: isService, states: newStateStore()}
}

// Run запускает бота: тест почты, обработка команд и рассылка уведомлений.
func (b *Bot) Run(ctx context.Context) {
	ctx, b.stop = context.WithCancel(ctx)
	// Тест подключения к IMAP при старте
	if err := b.worker.TestConnection(); err != nil {
		msg := fmt.Sprintf("⚠️ Не удалось подключиться к почте при старте: `%v`", err)
		log.Printf("[BOT] %s", msg)
		for _, id := range b.cfg.AdminUserIDs {
			b.sendToUser(ctx, id, msg)
		}
	} else {
		msg := fmt.Sprintf("✅ Бот запущен. Почта `%s` подключена.", b.cfg.IMAPUser)
		log.Printf("[BOT] %s", msg)
		for _, id := range b.cfg.AdminUserIDs {
			b.sendToUser(ctx, id, msg)
		}
	}

	b.RunBackupScheduler(ctx)
	b.registerCommands(ctx)

	updates := b.api.GetUpdates(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.events:
			if !ok {
				return
			}
			b.handleEvent(ctx, ev)
		case upd, ok := <-updates:
			if !ok {
				return
			}
			switch u := upd.(type) {
			case *schemes.MessageCreatedUpdate:
				b.handleMessage(ctx, u)
			case *schemes.MessageCallbackUpdate:
				b.handleCallback(ctx, u)
			}
		}
	}
}

// send отправляет сообщение в чат (для ответов на входящие сообщения).
func (b *Bot) send(ctx context.Context, chatID int64, text string) {
	if err := b.api.Messages.Send(ctx, maxbot.NewMessage().SetChat(chatID).SetText(text).SetFormat(schemes.Markdown)); err != nil {
		log.Printf("[BOT] ошибка отправки в chat=%d: %v", chatID, err)
	}
}

// sendToUser отправляет сообщение конкретному пользователю по его userID.
func (b *Bot) sendToUser(ctx context.Context, userID int64, text string) {
	if err := b.api.Messages.Send(ctx, maxbot.NewMessage().SetUser(userID).SetText(text).SetFormat(schemes.Markdown)); err != nil {
		log.Printf("[BOT] ошибка отправки пользователю user=%d: %v", userID, err)
	}
}

// broadcast рассылает сообщение списку пользователей.
func (b *Bot) broadcast(ctx context.Context, userIDs []int64, text string) {
	for _, uid := range userIDs {
		b.sendToUser(ctx, uid, text)
	}
}

// handleEvent рассылает уведомление о событии бэкапа.
func (b *Bot) handleEvent(ctx context.Context, ev email.Event) {
	ts := ev.Event.ReceivedAt.Format("2006-01-02 15:04")
	var text string
	switch ev.Event.Status {
	case "success":
		text = fmt.Sprintf("✅ **%s** — успешно\n`%s`", ev.Event.JobName, ts)
	case "failure":
		text = fmt.Sprintf("❌ **%s** — ошибка\n_%s_\n`%s`", ev.Event.JobName, ev.Event.Message, ts)
	case "missed":
		text = fmt.Sprintf("⚠️ **%s** — %s\n`%s`", ev.Event.JobName, ev.Event.Message, ts)
	}

	// Уведомить всех администраторов всегда
	seen := make(map[int64]bool)
	recipients := make([]int64, 0, len(b.cfg.AdminUserIDs))
	for _, id := range b.cfg.AdminUserIDs {
		seen[id] = true
		recipients = append(recipients, id)
	}

	// Если задача привязана к организации — уведомить подписчиков
	if ev.Job.OrgID != nil {
		subs, err := b.db.GetSubscribersForOrg(*ev.Job.OrgID)
		if err != nil {
			log.Printf("[BOT] ошибка получения подписчиков: %v", err)
		}
		for _, u := range subs {
			if !seen[u.ID] {
				seen[u.ID] = true
				recipients = append(recipients, u.ID)
			}
		}
	}

	b.broadcast(ctx, recipients, text)

	// Уведомить всех администраторов о новой задаче с кнопками привязки
	if ev.IsNewJob {
		for _, id := range b.cfg.AdminUserIDs {
			b.sendNewJobBindMessage(ctx, id, ev.Job.JobName)
		}
	}
}

// handleMessage маршрутизирует входящее сообщение к нужному обработчику.
// sendNewJobBindMessage отправляет администратору сообщение с кнопками привязки новой задачи к организации.
func (b *Bot) sendNewJobBindMessage(ctx context.Context, adminID int64, jobName string) {
	orgs, err := b.db.ListOrgs()
	if err != nil {
		log.Printf("[BOT] ошибка получения организаций: %v", err)
		return
	}

	kb := &maxbot.Keyboard{}
	// По 2 организации в ряд
	var row *maxbot.KeyboardRow
	for i, org := range orgs {
		if i%2 == 0 {
			row = kb.AddRow()
		}
		payload := fmt.Sprintf("bind:%s:%d", jobName, org.ID)
		row.AddCallback(org.Name, schemes.DEFAULT, payload)
	}
	// Кнопка создания новой организации отдельной строкой
	newRow := kb.AddRow()
	newRow.AddCallback("➕ Создать организацию", schemes.DEFAULT, fmt.Sprintf("neworg:%s", jobName))

	msg := fmt.Sprintf("⚙️ **Новая задача:** `%s`\nВыберите организацию для привязки:", jobName)
	if err := b.api.Messages.Send(ctx, maxbot.NewMessage().SetUser(adminID).SetText(msg).AddKeyboard(kb)); err != nil {
		log.Printf("[BOT] ошибка отправки bind-сообщения: %v", err)
	}
}

func (b *Bot) handleMessage(ctx context.Context, upd *schemes.MessageCreatedUpdate) {
	userID := upd.Message.Sender.UserId
	chatID := upd.Message.Recipient.ChatId
	name := upd.Message.Sender.Name
	username := upd.Message.Sender.Username
	text := strings.TrimSpace(upd.Message.Body.Text)

	log.Printf("[BOT] userId=%d text=%q", userID, text)

	// Обработка активного состояния (state machine)
	if b.cfg.IsAdmin(userID) {
		if st := b.states.get(userID); st.kind != stateNone {
			b.handleState(ctx, userID, chatID, text, st)
			return
		}
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])

	// Единственная публичная команда — /invite
	if cmd == "/invite" {
		b.cmdInvite(ctx, chatID, userID, name, username)
		return
	}

	// Администратор всегда имеет доступ ко всем командам
	isAdmin := b.cfg.IsAdmin(userID)

	if !isAdmin {
		// Проверка что пользователь активен — молча игнорируем незарегистрированных
		user, err := b.db.GetUser(userID)
		if err != nil {
			log.Printf("[BOT] ошибка получения пользователя %d: %v", userID, err)
			return
		}
		if user == nil || user.Status != "active" {
			return
		}
	}

	// Команды администратора
	if isAdmin {
		switch cmd {
		case "/pending", "/approve", "/reject", "/deactivate", "/users", "/orgs", "/addorg", "/setorg", "/checkmail", "/workdays", "/checkerrors", "/update", "/version", "/backupdb":
			b.handleAdminCmd(ctx, chatID, cmd, parts)
			return
		}
	}

	// Команды для активных пользователей
	switch cmd {
	case "/commands":
		b.cmdCommands(ctx, chatID, isAdmin)
	case "/stats":
		b.cmdStats(ctx, chatID, userID)
	case "/last":
		b.cmdLast(ctx, chatID, userID)
	case "/myorgs":
		b.cmdMyOrgs(ctx, chatID, userID)
	case "/subscribe":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /subscribe <org_name>")
			return
		}
		b.cmdSubscribe(ctx, chatID, userID, strings.Join(parts[1:], " "))
	case "/unsubscribe":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /unsubscribe <org_name>")
			return
		}
		b.cmdUnsubscribe(ctx, chatID, userID, strings.Join(parts[1:], " "))
	default:
		b.send(ctx, chatID, "Неизвестная команда.")
	}
}

func (b *Bot) handleAdminCmd(ctx context.Context, chatID int64, cmd string, parts []string) {
	switch cmd {
	case "/pending":
		b.cmdPending(ctx, chatID)
	case "/approve":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /approve <user_id>")
			return
		}
		b.cmdApproveReject(ctx, chatID, parts[1], "active")
	case "/reject":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /reject <user_id>")
			return
		}
		b.cmdApproveReject(ctx, chatID, parts[1], "rejected")
	case "/deactivate":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /deactivate <user_id>")
			return
		}
		b.cmdDeactivate(ctx, chatID, parts[1])
	case "/users":
		b.cmdUsers(ctx, chatID)
	case "/orgs":
		b.cmdOrgs(ctx, chatID)
	case "/addorg":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /addorg <name>")
			return
		}
		b.cmdAddOrg(ctx, chatID, strings.Join(parts[1:], " "))
	case "/setorg":
		b.cmdSetOrg(ctx, chatID, parts)
	case "/backupdb":
		b.cmdBackupDB(ctx, chatID)
	case "/version":
		b.cmdVersion(ctx, chatID)
	case "/update":
		b.cmdUpdate(ctx, chatID)
	case "/checkmail":
		b.cmdCheckMail(ctx, chatID, parts)
	case "/checkerrors":
		b.cmdCheckErrors(ctx, chatID)
	case "/workdays":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /workdays <org_name>")
			return
		}
		b.cmdWorkdays(ctx, chatID, strings.Join(parts[1:], " "))
	default:
		b.send(ctx, chatID, "Неизвестная команда.")
	}
}

func (b *Bot) cmdInvite(ctx context.Context, chatID, userID int64, name, username string) {
	existing, err := b.db.GetUser(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка сервера, попробуйте позже.")
		return
	}
	if existing != nil {
		switch existing.Status {
		case "pending":
			b.send(ctx, chatID, "Ваша заявка уже отправлена, ожидайте подтверждения.")
		case "active":
			b.send(ctx, chatID, "У вас уже есть доступ.")
		case "rejected":
			b.send(ctx, chatID, "Ваша заявка была отклонена.")
		}
		return
	}

	if err := b.db.CreateUser(userID, name, username); err != nil {
		b.send(ctx, chatID, "Ошибка сервера, попробуйте позже.")
		return
	}

	b.send(ctx, chatID, "Заявка отправлена! Ожидайте подтверждения от администратора.")

	usernameStr := ""
	if username != "" {
		usernameStr = " (@" + username + ")"
	}
	for _, id := range b.cfg.AdminUserIDs {
		b.sendToUser(ctx, id, fmt.Sprintf(
			"👤 **Новый запрос доступа**\n**%s**%s\nID: `%d`\n`/approve %d` | `/reject %d`",
			name, usernameStr, userID, userID, userID,
		))
	}
}

func (b *Bot) cmdStats(ctx context.Context, chatID, userID int64) {
	orgs, err := b.db.ListUserOrgs(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}

	var orgIDs []int64
	for _, o := range orgs {
		orgIDs = append(orgIDs, o.ID)
	}

	// Администратор видит всё
	if b.cfg.IsAdmin(userID) {
		orgIDs = nil
	}

	stats, err := b.db.GetStats(orgIDs)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения статистики.")
		return
	}
	if len(stats) == 0 {
		b.send(ctx, chatID, "Нет данных. Подпишитесь на организации: /subscribe <org_name>")
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 Статистика бэкапов\n")

	currentOrg := "\x00" // sentinel
	for _, s := range stats {
		orgLabel := s.OrgName
		if orgLabel == "" {
			orgLabel = "Без организации"
		}
		if orgLabel != currentOrg {
			sb.WriteString(fmt.Sprintf("\n🏢 %s\n", orgLabel))
			currentOrg = orgLabel
		}
		sb.WriteString(fmt.Sprintf(
			"  %s\n    7д:  ✅%d ❌%d ⚠️%d\n    30д: ✅%d ❌%d ⚠️%d\n",
			s.JobName,
			s.Success7, s.Failure7, s.Missed7,
			s.Success30, s.Failure30, s.Missed30,
		))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdLast(ctx context.Context, chatID, userID int64) {
	orgs, err := b.db.ListUserOrgs(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}

	var orgIDs []int64
	for _, o := range orgs {
		orgIDs = append(orgIDs, o.ID)
	}
	if b.cfg.IsAdmin(userID) {
		orgIDs = nil
	}

	events, err := b.db.GetLastEvents(orgIDs, 10)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения событий.")
		return
	}
	if len(events) == 0 {
		b.send(ctx, chatID, "Событий нет.")
		return
	}

	var sb strings.Builder
	sb.WriteString("**Последние события:**\n\n")
	for _, e := range events {
		ts := e.ReceivedAt.Format("2006-01-02 15:04")
		icon := "✅"
		if e.Status == "failure" {
			icon = "❌"
		} else if e.Status == "missed" {
			icon = "⚠️"
		}
		sb.WriteString(fmt.Sprintf("%s **%s** — `%s`\n", icon, e.JobName, ts))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdMyOrgs(ctx context.Context, chatID, userID int64) {
	orgs, err := b.db.ListUserOrgs(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(orgs) == 0 {
		b.send(ctx, chatID, "Вы не подписаны ни на одну организацию.\nИспользуйте `/subscribe <org_name>`")
		return
	}
	var sb strings.Builder
	sb.WriteString("**Ваши организации:**\n\n")
	for _, o := range orgs {
		sb.WriteString("• **" + o.Name + "**\n")
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdSubscribe(ctx context.Context, chatID, userID int64, orgName string) {
	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация **%s** не найдена.", orgName))
		return
	}
	if err := b.db.SubscribeUserToOrg(userID, org.ID); err != nil {
		b.send(ctx, chatID, "Ошибка подписки.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("✅ Вы подписались на **%s**.", orgName))
}

func (b *Bot) cmdUnsubscribe(ctx context.Context, chatID, userID int64, orgName string) {
	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация **%s** не найдена.", orgName))
		return
	}
	if err := b.db.UnsubscribeUserFromOrg(userID, org.ID); err != nil {
		b.send(ctx, chatID, "Ошибка отписки.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("✅ Вы отписались от **%s**.", orgName))
}

func (b *Bot) cmdPending(ctx context.Context, chatID int64) {
	users, err := b.db.ListUsersByStatus("pending")
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(users) == 0 {
		b.send(ctx, chatID, "Нет ожидающих заявок.")
		return
	}
	var sb strings.Builder
	sb.WriteString("**Ожидающие заявки:**\n\n")
	for _, u := range users {
		uname := ""
		if u.Username != "" {
			uname = " (@" + u.Username + ")"
		}
		sb.WriteString(fmt.Sprintf("**%s**%s — `%d`\n`/approve %d` | `/reject %d`\n\n",
			u.Name, uname, u.ID, u.ID, u.ID))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdApproveReject(ctx context.Context, chatID int64, userIDStr, status string) {
	var targetID int64
	if _, err := fmt.Sscanf(userIDStr, "%d", &targetID); err != nil {
		b.send(ctx, chatID, "Неверный user_id.")
		return
	}
	user, err := b.db.GetUser(targetID)
	if err != nil || user == nil {
		b.send(ctx, chatID, "Пользователь не найден.")
		return
	}
	if err := b.db.SetUserStatus(targetID, status); err != nil {
		b.send(ctx, chatID, "Ошибка обновления статуса.")
		return
	}
	action := "подтверждён"
	userNotif := "✅ Ваша заявка одобрена! Теперь вы можете получать уведомления.\nПодпишитесь на организации: `/subscribe <org_name>`"
	if status == "rejected" {
		action = "отклонён"
		userNotif = "❌ Ваша заявка была отклонена."
	}
	b.send(ctx, chatID, fmt.Sprintf("Пользователь **%s** %s.", user.Name, action))
	b.sendToUser(ctx, targetID, userNotif)
}

func (b *Bot) cmdDeactivate(ctx context.Context, chatID int64, userIDStr string) {
	var targetID int64
	if _, err := fmt.Sscanf(userIDStr, "%d", &targetID); err != nil {
		b.send(ctx, chatID, "Неверный user_id.")
		return
	}
	user, err := b.db.GetUser(targetID)
	if err != nil || user == nil {
		b.send(ctx, chatID, "Пользователь не найден.")
		return
	}
	if err := b.db.SetUserStatus(targetID, "inactive"); err != nil {
		b.send(ctx, chatID, "Ошибка обновления статуса.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("Пользователь %s деактивирован.", user.Name))
	b.sendToUser(ctx, targetID, "Ваш доступ был отозван.")
}

func (b *Bot) cmdUsers(ctx context.Context, chatID int64) {
	users, err := b.db.ListActiveUsers()
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(users) == 0 {
		b.send(ctx, chatID, "Нет активных пользователей.")
		return
	}
	var sb strings.Builder
	sb.WriteString("**Активные пользователи:**\n\n")
	for _, u := range users {
		orgs, _ := b.db.ListUserOrgs(u.ID)
		orgNames := make([]string, 0, len(orgs))
		for _, o := range orgs {
			orgNames = append(orgNames, o.Name)
		}
		orgStr := "_нет подписок_"
		if len(orgNames) > 0 {
			orgStr = strings.Join(orgNames, ", ")
		}
		uname := ""
		if u.Username != "" {
			uname = " (@" + u.Username + ")"
		}
		sb.WriteString(fmt.Sprintf("**%s**%s — `%d`\n%s\n\n", u.Name, uname, u.ID, orgStr))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdOrgs(ctx context.Context, chatID int64) {
	orgs, err := b.db.ListOrgs()
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(orgs) == 0 {
		b.send(ctx, chatID, "Нет организаций. Создайте: /addorg <name>")
		return
	}
	jobs, err := b.db.ListAllJobs()
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения задач.")
		return
	}

	jobsByOrg := make(map[int64][]string)
	for _, j := range jobs {
		if j.OrgID != nil {
			jobsByOrg[*j.OrgID] = append(jobsByOrg[*j.OrgID], j.JobName)
		}
	}

	var sb strings.Builder
	sb.WriteString("**Организации:**\n\n")
	for _, o := range orgs {
		jobNames := jobsByOrg[o.ID]
		jobStr := "_нет задач_"
		if len(jobNames) > 0 {
			quoted := make([]string, len(jobNames))
			for i, jn := range jobNames {
				quoted[i] = "`" + jn + "`"
			}
			jobStr = strings.Join(quoted, ", ")
		}
		sb.WriteString(fmt.Sprintf("🏢 **%s**\n%s\n\n", o.Name, jobStr))
	}

	var unassigned []string
	for _, j := range jobs {
		if j.OrgID == nil {
			unassigned = append(unassigned, j.JobName)
		}
	}
	if len(unassigned) > 0 {
		sb.WriteString("**Без организации:**\n")
		for _, name := range unassigned {
			sb.WriteString("• `" + name + "`\n")
		}
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdAddOrg(ctx context.Context, chatID int64, name string) {
	if err := b.db.CreateOrg(name); err != nil {
		b.send(ctx, chatID, fmt.Sprintf("Ошибка создания организации (возможно, уже существует): %v", err))
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("✅ Организация **%s** создана.", name))
}

func (b *Bot) cmdSetOrg(ctx context.Context, chatID int64, parts []string) {
	// Синтаксис: /setorg "job name" org_name
	// или: /setorg job_name org_name (если без пробелов)
	raw := strings.Join(parts[1:], " ")
	var jobName, orgName string

	if strings.HasPrefix(raw, `"`) {
		end := strings.Index(raw[1:], `"`)
		if end < 0 {
			b.send(ctx, chatID, `Использование: /setorg "название задачи" название_орг`)
			return
		}
		jobName = raw[1 : end+1]
		orgName = strings.TrimSpace(raw[end+2:])
	} else {
		fields := strings.Fields(raw)
		if len(fields) < 2 {
			b.send(ctx, chatID, `Использование: /setorg "название задачи" название_орг`)
			return
		}
		jobName = fields[0]
		orgName = strings.Join(fields[1:], " ")
	}

	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация %q не найдена.", orgName))
		return
	}
	if err := b.db.SetJobOrg(jobName, org.ID); err != nil {
		b.send(ctx, chatID, "Ошибка привязки задачи.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("Задача %q привязана к организации %q.", jobName, orgName))
}

func (b *Bot) cmdCheckMail(ctx context.Context, chatID int64, parts []string) {
	// /checkmail <дата> — показать события из БД за дату
	if len(parts) >= 2 {
		date, err := time.Parse("2006-01-02", parts[1])
		if err != nil {
			b.send(ctx, chatID, "Неверный формат даты. Используйте: `/checkmail 2026-04-02`")
			return
		}
		events, err := b.db.GetEventsByDate(date)
		if err != nil {
			b.send(ctx, chatID, fmt.Sprintf("❌ Ошибка получения событий: `%v`", err))
			return
		}
		if len(events) == 0 {
			b.send(ctx, chatID, fmt.Sprintf("📭 Событий за `%s` нет.", parts[1]))
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 **События за `%s`:**\n\n", parts[1]))
		for _, e := range events {
			icon := "✅"
			if e.Status == "failure" {
				icon = "❌"
			} else if e.Status == "missed" {
				icon = "⚠️"
			}
			sb.WriteString(fmt.Sprintf("%s **%s** — `%s`\n", icon, e.JobName, e.ReceivedAt.Format("15:04")))
		}
		b.send(ctx, chatID, sb.String())
		return
	}

	// /checkmail — ручная проверка почты
	b.send(ctx, chatID, "📬 Проверяю почту...")
	go func() {
		n, err := b.worker.Poll(ctx)
		if err != nil {
			b.send(ctx, chatID, fmt.Sprintf("❌ Ошибка проверки почты: %v", err))
			return
		}
		if n == 0 {
			b.send(ctx, chatID, "📭 Новых писем нет.")
		} else {
			b.send(ctx, chatID, fmt.Sprintf("✅ Обработано писем: %d.", n))
		}
	}()
}

func (b *Bot) cmdCheckErrors(ctx context.Context, chatID int64) {
	events, err := b.db.GetRecentErrors(24)
	if err != nil {
		b.send(ctx, chatID, fmt.Sprintf("❌ Ошибка получения данных: %v", err))
		return
	}
	if len(events) == 0 {
		b.send(ctx, chatID, "✅ Ошибок за последние 24 часа нет.")
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚠️ **Проблемы за последние 24ч** (%d):\n\n", len(events)))
	for _, e := range events {
		icon := "❌"
		if e.Status == "missed" {
			icon = "⚠️"
		}
		sb.WriteString(fmt.Sprintf("%s **%s** — `%s`\n", icon, e.JobName, e.ReceivedAt.Format("02.01 15:04")))
	}
	b.send(ctx, chatID, sb.String())
}

var weekdays = []string{"Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"}

func (b *Bot) cmdWorkdays(ctx context.Context, chatID int64, orgName string) {
	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация **%s** не найдена.", orgName))
		return
	}

	msg, kb := b.buildWorkdaysMessage(org)
	if err := b.api.Messages.Send(ctx, maxbot.NewMessage().SetChat(chatID).SetText(msg).AddKeyboard(kb)); err != nil {
		log.Printf("[BOT] ошибка отправки workdays: %v", err)
	}
}

func (b *Bot) buildWorkdaysMessage(org *db.Organization) (string, *maxbot.Keyboard) {
	days, _ := b.db.GetOrgWorkdays(org.ID)
	active := make(map[int]bool, len(days))
	for _, d := range days {
		active[d] = true
	}

	allActive := len(days) == 0
	kb := &maxbot.Keyboard{}
	row := kb.AddRow()
	for i, name := range weekdays {
		dayNum := i + 1
		label := name
		if allActive || active[dayNum] {
			label = "✅ " + name
		}
		payload := fmt.Sprintf("wd:%d:%d", org.ID, dayNum)
		row.AddCallback(label, schemes.DEFAULT, payload)
	}

	status := "все дни"
	if len(days) > 0 {
		names := make([]string, 0, len(days))
		for _, d := range days {
			names = append(names, weekdays[d-1])
		}
		status = strings.Join(names, ", ")
	}
	msg := fmt.Sprintf("🗓 **Рабочие дни для %s:**\n`%s`\nНажмите день чтобы включить/выключить:", org.Name, status)
	return msg, kb
}

func (b *Bot) handleCallback(ctx context.Context, upd *schemes.MessageCallbackUpdate) {
	payload := upd.Callback.Payload
	userID := upd.Callback.User.UserId

	if !b.cfg.IsAdmin(userID) {
		return
	}

	// Обработка wd:<org_id>:<day>
	if strings.HasPrefix(payload, "wd:") {
		var orgID int64
		var day int
		if _, err := fmt.Sscanf(payload, "wd:%d:%d", &orgID, &day); err != nil {
			return
		}

		added, err := b.db.ToggleOrgWorkday(orgID, day)
		if err != nil {
			log.Printf("[BOT] ошибка переключения рабочего дня: %v", err)
			return
		}

		action := "удалён"
		if added {
			action = "добавлен"
		}

		org, err := b.db.GetOrgByID(orgID)
		if err != nil || org == nil {
			return
		}

		// Ответить на callback чтобы убрать индикатор загрузки
		b.api.Messages.AnswerOnCallback(ctx, upd.Callback.CallbackID, &schemes.CallbackAnswer{})

		// Уведомление + обновлённая клавиатура
		chatID := upd.GetChatID()
		b.send(ctx, chatID, fmt.Sprintf("%s %s для %q.", weekdays[day-1], action, org.Name))
		msg, kb := b.buildWorkdaysMessage(org)
		if err := b.api.Messages.Send(ctx, maxbot.NewMessage().SetChat(chatID).SetText(msg).AddKeyboard(kb)); err != nil {
			log.Printf("[BOT] ошибка обновления workdays: %v", err)
		}
		return
	}

	chatID := upd.GetChatID()
	b.api.Messages.AnswerOnCallback(ctx, upd.Callback.CallbackID, &schemes.CallbackAnswer{})

	// bind:<jobName>:<orgID> — привязать задачу к организации
	if strings.HasPrefix(payload, "bind:") {
		// payload: bind:<jobName>:<orgID>
		// jobName может содержать двоеточия, поэтому парсим с конца
		lastColon := strings.LastIndex(payload, ":")
		if lastColon < 5 {
			return
		}
		jobName := payload[5:lastColon]
		var orgID int64
		if _, err := fmt.Sscanf(payload[lastColon+1:], "%d", &orgID); err != nil {
			return
		}
		// Проверяем — вдруг другой админ уже привязал
		job, err := b.db.GetJobByName(jobName)
		if err == nil && job != nil && job.OrgID != nil {
			existingOrg, _ := b.db.GetOrgByID(*job.OrgID)
			if existingOrg != nil {
				b.send(ctx, chatID, fmt.Sprintf("ℹ️ Задача %q уже привязана к организации %q.", jobName, existingOrg.Name))
				return
			}
		}
		org, err := b.db.GetOrgByID(orgID)
		if err != nil || org == nil {
			b.send(ctx, chatID, "Организация не найдена.")
			return
		}
		if err := b.db.SetJobOrg(jobName, orgID); err != nil {
			b.send(ctx, chatID, fmt.Sprintf("Ошибка привязки: %v", err))
			return
		}
		b.send(ctx, chatID, fmt.Sprintf("✅ Задача %q привязана к организации %q.", jobName, org.Name))
		return
	}

	// neworg:<jobName> — запросить название новой организации
	if strings.HasPrefix(payload, "neworg:") {
		jobName := payload[7:]
		b.states.set(userID, userState{kind: stateAwaitingOrgName, payload: jobName})
		b.send(ctx, chatID, fmt.Sprintf("Введите название новой организации для задачи %q:", jobName))
		return
	}
}

// handleState обрабатывает входящее сообщение когда пользователь находится в активном состоянии.
func (b *Bot) handleState(ctx context.Context, userID, chatID int64, text string, st userState) {
	switch st.kind {
	case stateAwaitingOrgName:
		jobName := st.payload
		orgName := strings.TrimSpace(text)
		if orgName == "" {
			b.send(ctx, chatID, "Название не может быть пустым. Попробуйте ещё раз:")
			return
		}
		b.states.clear(userID)

		// Проверяем — вдруг другой админ уже привязал пока вводили название
		job, err := b.db.GetJobByName(jobName)
		if err == nil && job != nil && job.OrgID != nil {
			existingOrg, _ := b.db.GetOrgByID(*job.OrgID)
			if existingOrg != nil {
				b.send(ctx, chatID, fmt.Sprintf("ℹ️ Задача %q уже привязана к организации %q.", jobName, existingOrg.Name))
				return
			}
		}

		// Создаём организацию (игнорируем ошибку если уже существует)
		_ = b.db.CreateOrg(orgName)

		org, err := b.db.GetOrgByName(orgName)
		if err != nil || org == nil {
			b.send(ctx, chatID, "Ошибка создания организации.")
			return
		}
		if err := b.db.SetJobOrg(jobName, org.ID); err != nil {
			b.send(ctx, chatID, fmt.Sprintf("Ошибка привязки задачи: %v", err))
			return
		}
		b.send(ctx, chatID, fmt.Sprintf("✅ Организация %q создана, задача %q привязана.", orgName, jobName))
	}
}

func (b *Bot) registerCommands(ctx context.Context) {
	commands := []schemes.BotCommand{
		{Name: "stats", Description: "Статистика бэкапов за 7 и 30 дней"},
		{Name: "last", Description: "Последние 10 событий"},
		{Name: "myorgs", Description: "Мои организации"},
		{Name: "subscribe", Description: "Подписаться на организацию"},
		{Name: "unsubscribe", Description: "Отписаться от организации"},
		{Name: "checkerrors", Description: "Ошибки за последние 24 часа"},
		{Name: "checkmail", Description: "Проверить почту / события за дату"},
		{Name: "backupdb", Description: "Отправить бэкап БД"},
		{Name: "workdays", Description: "Настроить рабочие дни организации"},
		{Name: "pending", Description: "Ожидающие заявки"},
		{Name: "approve", Description: "Одобрить пользователя"},
		{Name: "reject", Description: "Отклонить пользователя"},
		{Name: "deactivate", Description: "Деактивировать пользователя"},
		{Name: "users", Description: "Список активных пользователей"},
		{Name: "orgs", Description: "Список организаций и задач"},
		{Name: "addorg", Description: "Создать организацию"},
		{Name: "setorg", Description: "Привязать задачу к организации"},
		{Name: "version", Description: "Текущая версия бота"},
		{Name: "update", Description: "Обновить бота"},
	}
	if _, err := b.api.Bots.PatchBot(ctx, &schemes.BotPatch{Commands: commands}); err != nil {
		log.Printf("[BOT] ошибка регистрации команд: %v", err)
	} else {
		log.Printf("[BOT] команды зарегистрированы (%d шт.)", len(commands))
	}
}

func (b *Bot) cmdCommands(ctx context.Context, chatID int64, isAdmin bool) {
	var sb strings.Builder
	sb.WriteString("📋 **Доступные команды:**\n\n")

	sb.WriteString("**Для пользователей:**\n")
	sb.WriteString("`/stats` — статистика за 7 и 30 дней\n")
	sb.WriteString("`/last` — последние 10 событий\n")
	sb.WriteString("`/myorgs` — мои организации\n")
	sb.WriteString("`/subscribe <org>` — подписаться\n")
	sb.WriteString("`/unsubscribe <org>` — отписаться\n")

	if isAdmin {
		sb.WriteString("\n**Администратор:**\n")
		sb.WriteString("`/pending` — ожидающие заявки\n")
		sb.WriteString("`/approve <id>` — одобрить пользователя\n")
		sb.WriteString("`/reject <id>` — отклонить пользователя\n")
		sb.WriteString("`/deactivate <id>` — деактивировать пользователя\n")
		sb.WriteString("`/users` — список активных пользователей\n")
		sb.WriteString("`/orgs` — список организаций и задач\n")
		sb.WriteString("`/addorg <name>` — создать организацию\n")
		sb.WriteString("`/setorg \"<job>\" <org>` — привязать задачу к орг.\n")
		sb.WriteString("`/workdays <org>` — настроить рабочие дни\n")
		sb.WriteString("`/checkerrors` — ошибки за последние 24ч\n")
		sb.WriteString("`/checkmail` — проверить почту сейчас\n")
		sb.WriteString("`/checkmail <дата>` — события за дату\n")
		sb.WriteString("`/backupdb` — отправить бэкап БД\n")
		sb.WriteString("`/version` — текущая версия\n")
		sb.WriteString("`/update` — обновить бота\n")
	}

	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdBackupDB(ctx context.Context, chatID int64) {
	b.send(ctx, chatID, "📤 Отправляю бэкап БД...")
	go func() {
		b.sendDBBackup(ctx, fmt.Sprintf("🗄 Бэкап БД по запросу — %s", time.Now().In(b.cfg.Location).Format("2006-01-02 15:04")))
	}()
}

func (b *Bot) cmdVersion(ctx context.Context, chatID int64) {
	latest, err := updater.CheckLatestVersion(ctx)
	if err != nil {
		b.send(ctx, chatID, fmt.Sprintf("Текущая версия: `%s`\n⚠️ Не удалось проверить последнюю версию: `%v`", b.version, err))
		return
	}
	if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(b.version, "v") {
		b.send(ctx, chatID, fmt.Sprintf("✅ Установлена последняя версия: `%s`", b.version))
	} else {
		b.send(ctx, chatID, fmt.Sprintf("Текущая версия: `%s`\n🆕 Доступна новая версия: `%s`\nОбновить: `/update`", b.version, latest))
	}
}

func (b *Bot) cmdUpdate(ctx context.Context, chatID int64) {
	latest, err := updater.CheckLatestVersion(ctx)
	if err != nil {
		b.send(ctx, chatID, fmt.Sprintf("❌ Не удалось проверить версию: %v", err))
		return
	}

	if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(b.version, "v") {
		b.send(ctx, chatID, fmt.Sprintf("✅ Уже установлена последняя версия: `%s`", b.version))
		return
	}

	b.send(ctx, chatID, fmt.Sprintf("⬇️ Скачиваю версию `%s`...", latest))

	if err := updater.Update(ctx, b.version, b.isService, "MaxNotificationBot"); err != nil {
		b.send(ctx, chatID, fmt.Sprintf("❌ Ошибка обновления: `%v`", err))
		return
	}

	b.send(ctx, chatID, fmt.Sprintf("✅ Версия `%s` скачана. Перезапускаюсь...", latest))
	log.Printf("[BOT] запуск обновления до %s, завершение процесса", latest)
	b.stop()
}
