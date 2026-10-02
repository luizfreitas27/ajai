-- The options a select or radio question offered when it was remembered, so an
-- answer given later from Telegram can be checked against them; and which
-- Telegram message shows which remembered answer, so a reply to that message
-- still reaches its answer after a daemon restart.
ALTER TABLE answers ADD COLUMN options TEXT NOT NULL DEFAULT '[]'; -- JSON array of option texts

CREATE TABLE telegram_messages (
    bot_id     TEXT NOT NULL,    -- message ids are counted per bot and chat
    chat_id    TEXT NOT NULL,
    message_id INTEGER NOT NULL,
    answer_id  TEXT NOT NULL,
    sent_at    TEXT NOT NULL,    -- links expire (ForgetTelegramMessages)
    PRIMARY KEY (bot_id, chat_id, message_id)
);
