-- Sliding Window Rate Limiter
-- Atomic execution guaranteed by Redis — no race conditions possible.
--
-- KEYS[1] — the user's sorted set key e.g. "ratelimit:user:<user_id>"
-- ARGV[1] — current timestamp in milliseconds
-- ARGV[2] — window size in milliseconds  
-- ARGV[3] — maximum requests allowed in window (rpm limit)
-- ARGV[4] — throttle threshold (absolute count at which throttling begins)
--
-- Return value: table with three elements
--   [1] decision  — 0=reject, 1=allow, 2=throttle
--   [2] remaining — requests remaining in current window
--   [3] count     — current request count before this request

local key               = KEYS[1]
local now_ms            = tonumber(ARGV[1])
local window_ms         = tonumber(ARGV[2])
local limit             = tonumber(ARGV[3])
local throttle_threshold = tonumber(ARGV[4])

-- Step 1: Remove all timestamps that have fallen outside the current window.
-- ZREMRANGEBYSCORE removes members with score < (now - window).
-- After this call the set contains only requests made within the window.
local window_start = now_ms - window_ms
redis.call('ZREMRANGEBYSCORE', key, '-inf', window_start)

-- Step 2: Count how many requests remain in the window.
-- This is the user's current request count BEFORE we add the new one.
local current_count = redis.call('ZCARD', key)

-- Step 3: Decision logic.
-- Check against limit first — rejection is cheaper than throttle because
-- we skip the ZADD entirely for rejected requests.
if current_count >= limit then
    -- Over limit — reject without recording this attempt.
    -- We do NOT add this timestamp to the set: rejected requests do not
    -- consume quota, which prevents a thundering herd of 429s from
    -- permanently locking out a user who stops sending requests.
    local remaining = 0
    return {0, remaining, current_count}
end

-- Step 4: Record this request by adding the current timestamp to the ZSET.
-- The score IS the timestamp — this is what makes range queries by time work.
-- We use a unique member (timestamp + random suffix) to handle the edge case
-- where two requests arrive at the exact same millisecond.
local member = now_ms .. '-' .. math.random(1, 999999)
redis.call('ZADD', key, now_ms, member)

-- Step 5: Set TTL on the ZSET so it auto-expires when idle.
-- Without this, sorted sets for inactive users accumulate in Redis forever.
-- TTL is the window size in seconds (rounded up by 1 for safety margin).
local ttl_seconds = math.ceil(window_ms / 1000) + 1
redis.call('EXPIRE', key, ttl_seconds)

-- Step 6: Return decision with updated count.
local new_count = current_count + 1
local remaining = limit - new_count

if current_count >= throttle_threshold then
    -- In throttle zone — request is allowed but caller will introduce delay.
    return {2, remaining, new_count}
end

-- Under throttle threshold — allow immediately.
return {1, remaining, new_count}