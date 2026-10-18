# Летри — ежедневная тренировка внимания

Летри — адаптивное приложение для коротких упражнений с кириллическими leet-текстами и таблицами Шульте. Библиотека содержит 520 отдельных оригинальных мини-текстов: стихи, короткие истории и практики внимания. Сложность растёт каждые 80 заданий — от статичных букв к мерцанию, вращению и смешанным эффектам.

## Запуск в разработке

Нужны Go 1.22+ и Node.js 22.18+ с npm. Для настройки почты или оплаты скопируй .env.example в .env: Go загрузит его при старте.

1. В первом терминале из корня проекта запусти: go run ./backend
2. Во втором терминале перейди в frontend, затем запусти npm install и npm run dev.
3. Открой URL Vite, обычно http://localhost:5173.

Vite проксирует /api на Go-сервер localhost:8080. Аккаунты и прогресс текстовых заданий хранятся в backend/data/app.json.

## Почта

В локальной разработке SMTP можно не задавать: почта используется как логин, а подтверждение пропускается. Для реальной отправки кода подтверждения заполни SMTP_HOST, SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD и SMTP_FROM.

В production выставь APP_ENV=production: регистрация не будет работать без полного SMTP-набора и SESSION_SECRET. Возьми .env.example за основу конфигурации. В реальном .env не оставляй примерные секреты.

## Telegram Mini App и Stars

Создай бота через BotFather и укажи его токен в `TELEGRAM_BOT_TOKEN`. Задай случайный секрет в `TELEGRAM_WEBHOOK_SECRET`, опубликуй приложение по HTTPS и установи webhook бота на `https://example.ru/api/telegram/webhook`, передав тот же секрет как `secret_token` в `setWebhook`. В настройках бота укажи HTTPS-адрес Mini App.

Внутри Telegram сервер проверяет подпись и срок действия `initData` с помощью токена бота, затем создаёт сессию с HttpOnly cookie. Счёт создаётся через Telegram Bot API в валюте `XTR`; подписка стоит 200 Stars за 30 дней. Доступ продлевается только после серверной проверки подписанного webhook, `pre_checkout_query`, валюты, суммы, владельца счёта и Telegram charge ID. Повторные уведомления обрабатываются идемпотентно. Платёж разовый, автоматического списания нет.

Обычный вход по электронной почте и локальная веб-разработка остаются доступны вне Telegram. Для оплаты требуется открыть приложение внутри Telegram.

## Сборка для Go-сервера

В папке frontend выполни npm install и npm run build. Затем из корня запусти go run ./backend. Go отдаёт интерфейс из frontend/dist и API на порту 8080.

Для production включи HTTPS, задай APP_SECURE_COOKIES=true, длинный случайный SESSION_SECRET, APP_ENV=production, SMTP и настройки Telegram. Не публикуй пароли, токены ботов и webhook-секреты.

## Деплой на Render

1. Подключи репозиторий к Render (Web Service, runtime Go).
2. **Build Command:** `bash build.sh`
3. **Start Command:** `./leetri`
4. **Health Check Path:** `/api/health`
5. Переменные окружения (минимум):
   - `APP_ENV=production`
   - `APP_SECURE_COOKIES=true`
   - `SESSION_SECRET` — длинный случайный секрет (≥32 символов)
   - `DATA_FILE=data/app.json`
   - `TELEGRAM_BOT_TOKEN` и `TELEGRAM_WEBHOOK_SECRET` — для Mini App и Stars
6. После деплоя открой `https://<сервис>.onrender.com/` — должен открыться интерфейс (не текст «API работает»).
7. В BotFather укажи URL Mini App: `https://<сервис>.onrender.com/`
8. Webhook: `https://<сервис>.onrender.com/api/telegram/webhook` с тем же `secret_token`.

На бесплатном плане Render диск эфемерный: данные `data/app.json` сбрасываются при redeploy. Для постоянного хранения подключи Persistent Disk или внешнюю БД.

Локальная проверка production-сборки:

```bash
cd frontend && npm ci && npm run build && cd ..
go run .
# открой http://localhost:8080
```
