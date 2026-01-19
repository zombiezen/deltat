select
  uuidhex("entries"."uuid") as "uuid",
  "entries"."start_time" as "start_time",
  "entries"."end_time" as "end_time",

  uuidhex("tasks"."uuid") as "task.uuid",
  "tasks"."description" as "task.description"
from
  "entries"
  join "tasks" on "entries"."task_uuid" = "tasks"."uuid"
where
  "entries"."start_time" < strftime('%FT%T', :max_time) and
  coalesce("entries"."end_time", :now) > strftime('%FT%T', :min_time)
order by
  "entries"."start_time" asc;
