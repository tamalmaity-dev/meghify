-- 010_resource_guard_defaults: tune the runaway-container guard for small VPSes.
--
-- The original policy (90% of host CPU sustained 10 minutes) could never fire
-- on a 2-core VPS: one pegged core is only 50% of the host, so a runaway
-- single-core process was never caught. Lower the threshold to 40% (just below
-- one core on two cores), throttle to half a core, and act sooner after a
-- container starts.
--
-- Only the untouched default row is rewritten. An operator who already saved a
-- policy is left alone.

UPDATE resource_guard_settings
   SET cpu_percent = 40,
       cpu_throttle_percent = 25,
       memory_percent = 60,
       host_memory_percent = 90,
       grace_seconds = 60
 WHERE id = 'panel'
   AND cpu_percent = 90
   AND cpu_throttle_percent = 50
   AND memory_percent = 90
   AND host_memory_percent = 95
   AND grace_seconds = 300;
