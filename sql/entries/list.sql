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
  uuidhex("entries"."uuid") as "uuid",
  "entries"."start_time" as "start_time",
  "entries"."end_time" as "end_time",
  "entries"."scheduled_end_time" as "scheduled_end_time",

  uuidhex("tasks"."uuid") as "task.uuid",
  "tasks"."description" as "task.description",
  (select json_group_array(l."name")
    from
      "task_labels" as tl
      join "labels" as l on l."id" = tl."label_id"
    where tl."task_uuid" = "tasks"."uuid"
    order by l."name") as "task.labels"
from
  "resolved_entries" as "entries"
  join "tasks" on "entries"."task_uuid" = "tasks"."uuid"
where
  (:max_time is null or "entries"."start_time" < strftime('%FT%T', :max_time)) and
  (:min_time is null or coalesce("entries"."end_time", :now) > strftime('%FT%T', :min_time))
order by
  "entries"."start_time" asc;
