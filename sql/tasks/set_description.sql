update "tasks"
set "description" = :description
where "uuid" = uuid(:uuid);
