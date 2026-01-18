update "entries"
set "end_time" = strftime('%FT%T', :now)
where "end_time" is null and "uuid" = uuid(:uuid);
