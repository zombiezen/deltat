insert into "task_labels" ("task_uuid", "label_id")
select
  uuid(:target_task_uuid),
  "label_id"
from "task_labels"
where "task_uuid" = uuid(:source_task_uuid)
on conflict do nothing;
