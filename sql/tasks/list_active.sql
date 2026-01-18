with
  "active_tasks" as (
    select
      "task_uuid" as "uuid",
      min("start_time") as "earliest_start_time",
      max("start_time") as "latest_start_time"
    from "entries"
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
