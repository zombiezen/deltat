with
  "resolved_entries" as (
    select
      "uuid" as "uuid",
      "task_uuid" as "task_uuid",
      "start_time" as "start_time",
      case
        when "end_time" is not null then
          "end_time"
        when "scheduled_end_time" <= strftime('%FT%T',:now) then
          "scheduled_end_time"
      end as "end_time",
      iif(
        "end_time" is null and "scheduled_end_time" > strftime('%FT%T',:now),
        "scheduled_end_time",
        null) as "scheduled_end_time"
    from "entries"
  )
select
  "start_time" as "start_time",
  "end_time" as "end_time",
  "scheduled_end_time" as "scheduled_end_time"
from "resolved_entries"
where "uuid" = uuid(:uuid);
