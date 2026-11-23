WITH incoming AS (
  SELECT (j->>'topicId') AS external_id,
         btrim(j->>'positionName') AS title, coalesce((j->>'status')::integer, 0) AS status,
         nullif(j->>'content','') AS description, nullif(j->>'content2','') AS requirements,
         nullif(j->>'content3','') AS benefits, nullif(j->>'content5','') AS extra_content,
         nullif(j->>'company','') AS company_name, nullif(j->>'companyId','0') AS company_external_id,
         nullif(j->>'companyIntroduction','') AS company_intro, nullif(j->>'companyLogo','') AS company_logo,
         nullif(j->>'companyWebsite','') AS company_website, nullif(j->>'companySizeName','') AS company_size,
         nullif(j->>'workTypeName','') AS work_type_name, nullif(j->>'officeModeName','') AS office_mode_name,
         coalesce(nullif(j->>'location',''), nullif(j->>'base','')) AS location, nullif(j->>'base','') AS base_location,
         CASE WHEN j->>'compensationMode' IS DISTINCT FROM 'negotiable' THEN nullif((j->>'minSalary')::numeric,0) END AS min_salary,
         CASE WHEN j->>'compensationMode' IS DISTINCT FROM 'negotiable' THEN nullif((j->>'maxSalary')::numeric,0) END AS max_salary,
         nullif(j->>'currency','') AS salary_currency, nullif(j->>'payPeriod','') AS pay_period,
         'https://dejob.ai/jobDetail?id=' || (j->>'topicId') AS source_url,
         CASE WHEN jsonb_typeof(j->'tags')='array' THEN j->'tags' ELSE '[]'::jsonb END AS tags,
         coalesce((j->>'isTopJob')::boolean,false) AS is_top_job,
         to_timestamp((j->>'createTime')::bigint / 1000.0) AS source_created_at
  FROM jsonb_array_elements($2::jsonb) j
), written AS (
  INSERT INTO public.job_listings AS l
    (source_id, source_slug, source_name, external_id, title, status, description, requirements, benefits, extra_content,
     company_name, company_external_id, company_intro, company_logo, company_website, company_size,
     work_type_name, office_mode_name, location, base_location, min_salary, max_salary, salary_currency, pay_period,
     source_url, tags, is_top_job, source_created_at)
  SELECT $1::uuid, 'dejob', 'DeJob', i.* FROM incoming i
  ON CONFLICT (source_id, external_id) DO UPDATE SET
    (title, status, description, requirements, benefits, extra_content, company_name, company_external_id, company_intro,
     company_logo, company_website, company_size, work_type_name, office_mode_name, location, base_location,
     min_salary, max_salary, salary_currency, pay_period, source_url, tags, is_top_job, source_created_at, last_synced_at) =
    (excluded.title, excluded.status, excluded.description, excluded.requirements, excluded.benefits, excluded.extra_content,
     excluded.company_name, excluded.company_external_id, excluded.company_intro, excluded.company_logo, excluded.company_website,
     excluded.company_size, excluded.work_type_name, excluded.office_mode_name, excluded.location, excluded.base_location,
     excluded.min_salary, excluded.max_salary, excluded.salary_currency, excluded.pay_period, excluded.source_url,
     excluded.tags, excluded.is_top_job, excluded.source_created_at, now())
  WHERE (l.title, l.status, l.description, l.requirements, l.benefits, l.extra_content, l.company_name, l.company_external_id,
    l.company_intro, l.company_logo, l.company_website, l.company_size, l.work_type_name, l.office_mode_name, l.location,
    l.base_location, l.min_salary, l.max_salary, l.salary_currency, l.pay_period, l.source_url, l.tags, l.is_top_job, l.source_created_at)
  IS DISTINCT FROM
    (excluded.title, excluded.status, excluded.description, excluded.requirements, excluded.benefits, excluded.extra_content,
     excluded.company_name, excluded.company_external_id, excluded.company_intro, excluded.company_logo, excluded.company_website,
     excluded.company_size, excluded.work_type_name, excluded.office_mode_name, excluded.location, excluded.base_location,
     excluded.min_salary, excluded.max_salary, excluded.salary_currency, excluded.pay_period, excluded.source_url,
     excluded.tags, excluded.is_top_job, excluded.source_created_at)
  RETURNING external_id
)
SELECT count(*) FILTER (WHERE old.id IS NULL), count(*) FILTER (WHERE old.id IS NOT NULL)
FROM written w LEFT JOIN public.job_listings old ON old.source_id=$1::uuid AND old.external_id=w.external_id;
