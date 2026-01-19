select
  "description" as "description"
from "tasks"
where "uuid" = uuid(:uuid)
limit 1;
