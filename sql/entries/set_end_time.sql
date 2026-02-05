update "entries"
set
  "end_time" = strftime('%FT%T', :time),
  "scheduled_end_time" = null
where "uuid" = uuid(:uuid);
