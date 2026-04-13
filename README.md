# lms-service

Learning Management System service. Manages courses, student enrollments, lesson progress tracking, quizzes, and certificate generation.

**Port:** `8083`

## Responsibilities

- Course and module/lesson CRUD
- Student enrollment and progress tracking (watch percentage, completion)
- Drip-feed content gating (`drip_days` per lesson)
- Quiz management and attempt scoring
- Certificate generation and regeneration

## Directory Structure

```
lms-service/
├── cmd/
│   └── main.go          # Entry point
├── routes/
│   └── lms.go           # All HTTP handlers (~1000 lines)
└── queries/
    └── lms.go           # MongoDB query layer
```

## API Endpoints

### Tenant (JWT required, `/api/lms/` prefix)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/lms/courses` | List courses |
| `POST` | `/api/lms/courses` | Create a course |
| `GET` | `/api/lms/courses/:id` | Get course details |
| `PUT` | `/api/lms/courses/:id` | Update a course |
| `DELETE` | `/api/lms/courses/:id` | Delete a course |
| `GET` | `/api/lms/enrollments` | List enrollments |
| `POST` | `/api/lms/enrollments` | Create an enrollment |
| `GET` | `/api/lms/enrollments/:id` | Get enrollment details |
| `POST` | `/api/lms/enrollments/:id/progress` | Record lesson progress |
| `POST` | `/api/lms/enrollments/:id/revoke` | Revoke an enrollment |
| `GET` | `/api/lms/quizzes` | List quizzes |
| `POST` | `/api/lms/quizzes/:id/attempt` | Submit a quiz attempt |
| `GET` | `/api/lms/quizzes/:id/attempts` | List attempt history |
| `GET` | `/api/lms/certificates` | List certificates |
| `POST` | `/api/lms/certificates` | Issue a certificate |
| `POST` | `/api/lms/certificates/:id/regenerate` | Regenerate a certificate |

### Internal (`/internal/` prefix, no auth)

Service-to-service endpoints for querying course and enrollment data (called by core-service and marketing-service).

## Data Models

All models live in `pkg/models/lms.go`.

**`CourseModule`** — A grouping of lessons within a course.
- `slug`, `title`, `order`, `lessons[]`, `quiz_slug`

**`CourseLesson`** — An individual lesson.
- `slug`, `title`, `order`, `video_url`, `duration`, `content_html`
- `is_free`, `is_draft`, `drip_days` — access control and scheduling
- `content_gen_status`, `content_gen_config` — AI content generation state

**`CourseEnrollment`** — A student's enrollment record.
- `contact_id`, `product_id`, `status` (`active` / `revoked`)
- `progress[]`, `overall_percent`, `enrolled_at`, `completed_at`, `expires_at`

**`LessonProgress`** — Per-lesson tracking within an enrollment.
- `watch_percent`, `last_position_sec`, `completed`, `quiz_passed`

**`Quiz`** — Assessment attached to a module.
- Questions with multiple-choice answers and correct-answer keys

**`QuizAttempt`** — Historical record of a quiz submission with score.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LMS_SERVICE_PORT` | `8083` | HTTP listen port |
| `MONGO_HOST` | — | MongoDB host |
| `MONGO_PORT` | — | MongoDB port |
| `MONGO_DB` | — | Database name |

## Dependencies

- [`gin-gonic/gin`](https://github.com/gin-gonic/gin) — HTTP framework
- `gopkg.in/mgo.v2` — MongoDB driver
- `../pkg` — Shared auth, config, db, models, HTTP utilities
