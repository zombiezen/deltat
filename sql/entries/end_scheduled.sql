update "entries"
set
  "end_time" = "scheduled_end_time",
  "scheduled_end_time" = null
where
  "end_time" is null and
  "scheduled_end_time" is not null and
  "scheduled_end_time" <= strftime('%FT%T',:now);
