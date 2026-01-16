insert into "labels" ("name") values (:name)
  on conflict ("name") do nothing;
