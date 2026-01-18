insert into "task_labels" (
  "task_uuid",
  "label_id"
) values (
  uuid(:task_uuid),
  (select "id" from "labels" where "name" = :label)
) on conflict ("task_uuid", "label_id") do nothing;
