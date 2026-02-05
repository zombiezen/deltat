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
  ),
  "active_tasks" as (
    select
      "task_uuid" as "uuid",
      min("start_time") as "earliest_start_time",
      max("start_time") as "latest_start_time"
    from "resolved_entries" as "entries"
    where "entries"."end_time" is null
    group by "task_uuid"
  )
select
  "tasks"."description" as "description",
  "active_tasks"."earliest_start_time" as "start_time"
from
  "active_tasks"
  join "tasks" using ("uuid")
order by "latest_start_time" desc
limit coalesce(:limit, -1);
