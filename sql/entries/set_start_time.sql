update "entries"
set "start_time" = strftime('%FT%T', :time)
where "uuid" = uuid(:uuid);
