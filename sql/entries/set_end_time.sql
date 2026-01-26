update "entries"
set "end_time" = strftime('%FT%T', :time)
where "uuid" = uuid(:uuid);
