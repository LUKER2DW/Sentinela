-- Require every existing account to choose its event notifications again.
-- New accounts use notify='' and the API's empty, opt-in default.
UPDATE users SET notify = '{"follow_mode":true,"types":[]}';
