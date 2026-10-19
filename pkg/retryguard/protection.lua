-- One bounded rolling window per group/config revision. Redis owns time and
-- transitions. No request payloads or per-request keys are stored.
local key = KEYS[1]
local now = tonumber(redis.call('TIME')[1])
local w, rpm, threshold = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
local initial, multiplier, cap, reset = tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6]), tonumber(ARGV[7])
local seq, requests, slow = ARGV[8], tonumber(ARGV[9]), tonumber(ARGV[10])
local v = redis.call('HMGET',key,'last','start','requests','slow','until','level','duration','recovered','epoch')
local last = tonumber(v[1]) or now
local start = tonumber(v[2]) or now
local nr, ns = tonumber(v[3]) or 0, tonumber(v[4]) or 0
local untilAt, level, duration = tonumber(v[5]) or 0, tonumber(v[6]) or 0, tonumber(v[7]) or 0
local recovered, epoch = tonumber(v[8]) or now, tonumber(v[9]) or 0
if now < last then return redis.error_reply('clock moved backwards') end
if now-last>5 then start=now;recovered=now end

-- Advance only elapsed slots. After an idle whole window, clear the bounded
-- hash once, preserving the cooldown state in locals.
if now-last >= w then
 redis.call('DEL',key)
 nr=0;ns=0;epoch=epoch+1
else
 for second=last+1,now do
  local slot=tostring(second%w)
  local old=redis.call('HMGET',key,'r:'..slot,'s:'..slot,'e:'..slot)
  nr=nr-(tonumber(old[1]) or 0)
  if tonumber(old[3])==epoch then ns=ns-(tonumber(old[2]) or 0) end
  redis.call('HDEL',key,'r:'..slot,'s:'..slot,'e:'..slot)
 end
end
local resumed=false
if untilAt>0 and now>=untilAt then
 recovered=math.max(untilAt,recovered);untilAt=0;ns=0;epoch=epoch+1;resumed=true
end
if untilAt==0 and now-recovered>=reset then level=0;duration=0 end

if (requests>0 or slow>0) and redis.call('GET',KEYS[2])~=seq then
 redis.call('SET',KEYS[2],seq,'EX',w+60)
 local slot=tostring(now%w)
 nr=nr+requests
 if requests>0 then redis.call('HINCRBY',key,'r:'..slot,requests) end
 -- A batch collected during cooldown cannot immediately trip it on recovery.
 if slow>0 and untilAt==0 and not resumed and now>recovered then
  if tonumber(redis.call('HGET',key,'e:'..slot))~=epoch then
   redis.call('HSET',key,'s:'..slot,0,'e:'..slot,epoch)
  end
  ns=ns+slow
  redis.call('HINCRBY',key,'s:'..slot,slow)
 end
end
local ready=0
if now-start>=w then ready=1 end
if ready==1 and untilAt==0 and nr*60>=rpm*w and ns>=threshold then
 if level==0 or multiplier==1 then duration=initial else duration=math.min(cap,duration*multiplier) end
 level=math.min(level+1,64)
 untilAt=now+duration
end
redis.call('HSET',key,'last',now,'start',start,'requests',nr,'slow',ns,'until',untilAt,'level',level,'duration',duration,'recovered',recovered,'epoch',epoch)
redis.call('EXPIRE',key,math.max(0,untilAt-now)+reset+w+60)
return {now,nr,ns,untilAt,level,duration,ready}
