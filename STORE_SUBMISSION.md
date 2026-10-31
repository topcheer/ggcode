# Store Submission via GitHub Pipeline — One-time Setup

The `store-submission.yml` workflow builds MSIX packages and submits them to
Microsoft Store certification automatically. It needs four repo secrets that
come from a one-time Azure AD / Partner Center configuration.

## One-time Partner Center + Entra setup (manual, ~15 min)

1. **Entra app registration** — portal.azure.com → Microsoft Entra ID →
   App registrations → New registration. Note the **Tenant ID** (Directory ID,
   on the Overview page) and **Client ID** (Application ID).
2. **Client secret** — in the app: Certificates & secrets → New client secret.
   Copy the secret VALUE immediately (shown once).
3. **Link the app to Partner Center** — Partner Center → Account settings →
   User management → Azure AD applications → Add the app you just created.
   Assign the **Developer** role (or a custom role with submission rights).
4. **Product ID** — Partner Center → your GGCode Desktop product → the
   product id is in the URL: `.../products/<PRODUCT_ID>/packages`. Note it.

## Repo secrets (Settings → Secrets → Actions)

| Secret | Value |
|---|---|
| `STORE_TENANT_ID` | Entra tenant (directory) id |
| `STORE_CLIENT_ID` | App registration application id |
| `STORE_CLIENT_SECRET` | Client secret value |
| `STORE_PRODUCT_ID` | Partner Center product id for GGCode Desktop |

## Usage

Actions → **store-submission** → Run workflow → enter the new four-part
version (e.g. `1.3.269.0`) and optional release notes. The run builds the
MSIX, uploads it, and commits the submission — certification then takes the
usual 1–3 business days.

## Notes & constraints

- Only ONE in-flight submission per product: wait for a submission to finish
  (publish or reject) before the next. Delete a pending submission in
  Partner Center if you need to abandon one.
- `submit-store.ps1` follows the documented v1.0 submission shape; if the
  live API rejects a field, adjust the PUT body in step 5 of the script.
- First run should be treated as a shakedown: watch the run log, and confirm
  the submission appears in Partner Center → Submissions.
