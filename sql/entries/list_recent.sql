select
  uuidhex("entries"."uuid") as "uuid",
  "entries"."start_time" as "start_time",
  "entries"."end_time" as "end_time",

  uuidhex("tasks"."uuid") as "task.uuid",
  "tasks"."description" as "task.description",
  (select json_group_array(l."name")
    from
      "task_labels" as tl
      join "labels" as l on l."id" = tl."label_id"
    where tl."task_uuid" = "tasks"."uuid"
    order by l."name") as "task.labels"
from
  "entries"
  join "tasks" on "entries"."task_uuid" = "tasks"."uuid"
order by
  "entries"."start_time" desc
limit :limit;
