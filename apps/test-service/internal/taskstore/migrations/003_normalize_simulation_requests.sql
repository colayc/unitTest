UPDATE tasks
SET request_json = json_object(
  'scenario', scenario,
  'timeoutMs', timeout_ms
)
WHERE kind = 'simulation'
  AND plan_fingerprint = '';
