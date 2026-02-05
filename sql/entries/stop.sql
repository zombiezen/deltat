update "entries"
set
  "end_time" = case
    when "scheduled_end_time" is not null and "scheduled_end_time" <= strftime('%FT%T',:now) then
      "scheduled_end_time"
    else
      strftime('%FT%T', :now)
    end,
  "scheduled_end_time" = null
where "end_time" is null and "uuid" = uuid(:uuid);
