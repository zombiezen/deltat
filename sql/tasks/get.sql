select
  "description" as "description",
  (select json_group_array(l."name")
    from
      "task_labels" as tl
      join "labels" as l on l."id" = tl."label_id"
    where tl."task_uuid" = "tasks"."uuid"
    order by l."name") as "labels",
  (select count(*) from "entries" where "entries"."task_uuid" = "tasks"."uuid") as "entry_count"
from "tasks"
where "uuid" = uuid(:uuid)
limit 1;
