# 배포 산출물 빌드와 플랫폼 검증

이 문서는 dcraft 실행 파일을 만드는 사람과 검증하는 사람을 위한 기록입니다. 사용자 시작 안내는
[install.md](install.md)에 있습니다. 이 산출물은 외부 릴리스로 게시되지 않았고, 자동 업데이트도 없습니다.

## 빌드

```sh
VERSION=v0.1.0 scripts/build.sh
```

| 환경 변수 | 기본값 | 의미 |
|---|---|---|
| `VERSION` | `dev` | ldflags로 주입하는 버전. 소스를 수정하지 않고 바꿀 수 있음 |
| `COMMIT` | git HEAD, 없으면 `unknown` | ldflags로 주입하는 소스 커밋 |
| `BUILD_DATE` | 커밋 시각, `SOURCE_DATE_EPOCH`, 없으면 `unknown` | ldflags로 주입하는 빌드 시각 |
| `DIST_DIR` | `<repo>/dist` | 출력 폴더. 비어 있어야 함 |
| `TARGETS` | 네 대상 전체 | 공백으로 구분한 부분 집합 |
| `GO` | `go` | 사용할 Go 도구 |

대상은 `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64` 네 가지입니다. Windows 네이티브 대상은
없습니다. WSL2는 Linux 실행 파일을 사용하는 경로로만 지원합니다.

출력:

```
dist/
  dcraft_<버전>_darwin_amd64
  dcraft_<버전>_darwin_arm64
  dcraft_<버전>_linux_amd64
  dcraft_<버전>_linux_arm64
  SHA256SUMS        # `<sha256>  <파일 이름>` 네 줄
  build-info.json   # version, commit, build_date, go_version, 파일별 플랫폼/해시
```

`scripts/build.sh`는 macOS와 Linux에서 같은 방식으로 실행됩니다. Windows에서는 WSL2 안에서 실행하세요.
검증 명령과 다른 점은 없습니다.

## 재현성과 식별

모든 산출물은 다음 고정 입력으로 빌드합니다.

- `-trimpath` — 빌드 머신의 절대 경로를 넣지 않음
- `-buildvcs=false` — 작업 트리 VCS 메타데이터를 넣지 않음; 커밋은 ldflags로만 기록
- `CGO_ENABLED=0` — 정적 Go 실행 파일, 컨테이너에 libc 외 런타임 불필요
- `-ldflags "-s -w -X ...Version=<VERSION> -X ...Commit=<COMMIT> -X ...BuildDate=<BUILD_DATE>"` — 결정적 식별
- 의존성 버전은 `go.mod`/`go.sum`으로 고정
- Go 도구 버전은 `build-info.json`의 `go_version`에 기록

같은 커밋, 같은 Go 도구, 깔끔한 작업 트리에서 다시 빌드하면 같은 바이트와 같은 식별이 나옵니다.
다른 Go 도구 버전은 바이트를 바꿀 수 있으므로 `go_version`을 함께 확인하세요. 작업 트리에 커밋되지 않은
변경이 있으면 스크립트가 경고합니다. 그 빌드의 `COMMIT`은 HEAD를 가리키므로 배포용으로 쓰지 마세요.

ldflags를 주지 않은 일반 `go build`는 `dev` / `none` / `unknown`으로 식별됩니다.

## 식별 확인

```sh
./dist/dcraft_dev_darwin_arm64 --version
# dcraft dev (commit ..., built ..., darwin/arm64, go1.27.4)
```

`--version`은 `cmd/dcraft` 프로세스 진입에서 처리하며 애플리케이션, Docker, 상태 폴더를 초기화하지 않습니다.
기능 레지스트리 명령을 추가하지 않았습니다. 기계 판독이 필요하면 `build-info.json`을 사용하세요.

무결성 확인:

```sh
cd dist
shasum -a 256 -c SHA256SUMS        # macOS
sha256sum -c SHA256SUMS            # Linux
```

`SHA256SUMS`와 `build-info.json`은 손상 여부를 확인합니다. 서명이나 배포자 신원을 증명하지 않습니다.

## 자동 검증

Docker 없이 실행되는 검사(네 대상 빌드, 파일 형식/아키텍처, `go version -m`, 내장 ldflags, 체크섬,
메타데이터, 네이티브 실행):

```sh
go test -count=1 ./test/dist
```

포함 내용:

- `TestBuildProducesFourArtifactsWithIntegrityMetadata` — 네 산출물, Mach-O/ELF 헤더와 CPU, `go version -m`의
  모듈/`-trimpath`/`CGO_ENABLED=0`/GOOS/GOARCH, 바이너리에 박힌 버전/커밋/날짜, `SHA256SUMS`,
  `build-info.json`, darwin/arm64 산출물의 `--help`/`--version`/빈 폴더 `status`/비대화형 무명령/TUI 실행
- `TestRebuildProducesIdenticalArtifactsAndIdentification` — 같은 입력으로 두 번 빌드해 체크섬과 메타데이터 동일
- `TestPlainBuildKeepsDevelopmentDefaults` — ldflags 없는 빌드가 `dev`/`none`/`unknown`으로 식별

Docker 실행 검사(명시적으로 켜고 Docker가 있을 때만):

```sh
DCRAFT_DOCKER_TEST=1 go test -count=1 ./test/dist -run TestLinuxArtifactsExecuteInBareContainers -v
```

이미지가 없으면 검사를 건너뜁니다. 먼저 `docker pull alpine:3`을 실행하면 됩니다.

이 검사는 `alpine:3`(Python/Node/Go 없음) 컨테이너에 산출물을 읽기 전용으로 넣고 linux/amd64,
linux/arm64 각각에서 `--help`, `--version`, 빈 폴더 `status`, Compose 파일이 있지만 Docker CLI가 없는
`status`의 `docker_missing` 진단, 비대화형 무명령 실패를 실행합니다. 컨테이너는 `--rm`으로 끝나며 Compose
프로젝트나 볼륨을 만들지 않습니다. 컨테이너 안에 Docker를 설치하지 않습니다.

## 검증 매트릭스

| 플랫폼 | 이번에 실제 실행 | 교차 빌드/검사만 | 비고 |
|---|---|---|---|
| darwin/arm64 | 예 | — | macOS Apple Silicon, 위 네이티브 검사 |
| linux/amd64 | 예 | — | Docker `alpine:3`, Rosetta/에뮬레이션 여부는 호스트에 따름 |
| linux/arm64 | 예 | — | Docker `alpine:3` |
| darwin/amd64 | 아니요 | 예 | 형식·메타데이터만 확인, 실행하지 않음 |
| WSL2 | 아니요 | — | 지원 사용 경로, 이번 환경에서 미검증 |
| Windows 네이티브 | 아니요 | — | 지원하지 않음 |

실행하지 않은 플랫폼은 검증 통과로 표시하지 않습니다.

## CI

`.github/workflows/dist-artifacts.yml`은 `v*` 태그 push에서 네 산출물을 빌드해 워크플로 아티팩트로
올립니다. GitHub Release를 만들지 않고, 게시하거나 배포하지 않으며, 저장소 비밀값을 쓰지 않습니다.
자동 업데이트와 자동 릴리스는 없습니다.
