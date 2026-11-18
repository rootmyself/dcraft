# dcraft 설치와 시작

이 문서는 받은 실행 파일을 처음 실행하는 데 필요한 최소 안내입니다. 전체 사용 설명서가 아닙니다.
이 배포물은 상태 조회 흐름을 검증한 개발 단계 산출물이며, 전체 제품 완료나 정식 출시를 뜻하지 않습니다.

## 1. 내 컴퓨터에 맞는 파일 고르기

| 환경 | 받을 파일 |
|---|---|
| macOS Apple Silicon (M1 이상) | `dcraft_<버전>_darwin_arm64` |
| macOS Intel | `dcraft_<버전>_darwin_amd64` |
| Linux x86_64 | `dcraft_<버전>_linux_amd64` |
| Linux arm64 | `dcraft_<버전>_linux_arm64` |
| WSL2 | WSL2 안에서 위의 Linux 파일 사용 |

Windows 네이티브 실행은 지원하지 않습니다. WSL2는 지원 사용 경로이지만 이번 검증 환경에서는 실제 실행하지 않았습니다.
`<버전>`은 받은 파일 이름과 `build-info.json`의 `version` 값으로 확인하세요.

## 2. 무결성 확인

실행 파일과 `SHA256SUMS`, `build-info.json`을 같은 폴더에 받습니다.

macOS:

```sh
shasum -a 256 dcraft_<버전>_darwin_arm64
# 출력된 해시를 SHA256SUMS에서 같은 파일 이름의 해시와 비교
```

Linux:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

`build-info.json`에는 버전, 커밋, 빌드 시각, Go 도구 버전, 대상 플랫폼과 파일별 SHA-256이 들어 있습니다.
이 확인은 파일 손상 여부만 알려 줍니다. 서명 검증은 아직 없습니다.

## 3. 실행 준비

```sh
mv dcraft_<버전>_darwin_arm64 dcraft   # 파일 이름은 받은 것에 맞게
chmod +x dcraft
./dcraft --version
```

`--version`은 `dcraft <버전> (commit ..., built ..., <os>/<arch>, <go 버전>)` 한 줄을 출력합니다.
Python, Node.js, Go 실행 환경은 필요하지 않습니다. 실행 파일 하나로 동작합니다.

macOS에서 "확인되지 않은 개발자" 경고로 열리지 않으면, 위 해시를 직접 확인한 뒤에만 다음을 실행하세요.

```sh
xattr -d com.apple.quarantine ./dcraft
```

## 4. 시작과 상태 확인

Compose 파일이 있는 프로젝트 폴더로 이동합니다.

```sh
cd ~/my-stack
./dcraft          # 대화형 화면(TUI). q 종료, r 새로고침
./dcraft status   # 같은 상태를 한 번만 출력
./dcraft status --json
./dcraft --help
```

파이프나 CI처럼 대화형 터미널이 아닌 곳에서 명령 없이 실행하면 TUI를 열지 않고 실패합니다.
그때는 `dcraft status`처럼 명령을 지정하세요. 종료 코드는 성공 0, 실행 실패 1, 사용법 오류 2, 중단 130입니다.

## 5. 외부 전제

- **Docker와 Compose가 필요합니다.** `docker --version`, `docker compose version`이 성공해야 합니다.
  `--help`와 `--version`만은 Docker 없이 동작합니다.
- **로컬 Docker Engine만 지원합니다.** `tcp://`, `ssh://`, `http(s)://` 원격 엔드포인트는 거부합니다.
  Docker Desktop의 로컬 VM 소켓은 로컬로 취급합니다.
- **AWS Secrets Manager 원본을 쓰려면 AWS CLI v2와 기존 AWS 인증이 필요합니다.** `aws secretsmanager
  get-secret-value`가 동작해야 하며, dcraft는 자격 증명을 저장하거나 `aws configure`를 대신 실행하지 않습니다.
  AWS 원본을 쓰지 않으면 AWS CLI는 필요하지 않습니다. Git은 `generate-git-hook`(호스트 Git 스크립트
  생성)에 필요하고, S3 배포 스크립트 생성은 아직 구현되지 않았습니다.
- dcraft는 Docker를 자동 설치하지 않고, 스스로 자동 업데이트하지 않습니다. 새 파일은 직접 내려받아 교체하세요.
- 도우미 상태는 `DCRAFT_STATE_DIR` 또는 macOS `~/Library/Application Support/dcraft`,
  Linux `~/.config/dcraft` 아래에 저장됩니다. 상태 조회는 프로젝트의 Compose 파일을 수정하지 않습니다.

## 6. 오류가 났을 때

| 화면의 메시지 | 다음 행동 |
|---|---|
| `Docker CLI를 찾을 수 없습니다.` | Docker Desktop(macOS) 또는 Docker Engine(Linux) 설치 후 새 터미널에서 `docker --version` 확인. WSL2는 Docker Desktop의 WSL 통합을 켜기 |
| `Docker Engine에 연결할 수 없습니다.` | Docker Desktop/서비스 시작 후 `docker info` 확인. 원격용 `DOCKER_HOST`가 있으면 해제 |
| `Docker Engine에 접근할 권한이 없습니다.` | Linux에서 docker 그룹과 `/var/run/docker.sock` 권한 확인. dcraft는 권한 상승을 대신하지 않음 |
| `Docker Compose를 사용할 수 없습니다.` | Compose 플러그인 설치 또는 Docker Desktop 업데이트 후 `docker compose version` 확인 |
| `원격 Docker Engine은 지원하지 않습니다.` | `docker context use`로 로컬 컨텍스트 선택, `DOCKER_HOST` 해제 |
| `대상 폴더에서 Compose 설정을 찾지 못했습니다.` | 폴더에 `compose.yaml`/`docker-compose.yml` 추가 또는 `--path` 지정. 상위 폴더는 자동 탐색하지 않음 |
| `대화형 터미널이 아니어서` | `dcraft status`처럼 명령 지정, 기계 판독은 `--json` 추가 |

## 7. 이번 검증 범위

| 대상 | 검증 방식 |
|---|---|
| darwin/arm64 | 실제 실행: `--help`, `--version`, 빈 폴더 `status`, 비대화형 무명령 실패, TUI 열기/종료 |
| linux/amd64 | 실제 실행: Docker `alpine:3` 컨테이너(Python/Node/Go 없음)에서 위 명령과 `status` 확인 |
| linux/arm64 | 실제 실행: linux/amd64와 같은 컨테이너 검증 |
| darwin/amd64 | 교차 빌드와 형식·메타데이터 검사만 수행, 실제 실행하지 않음 |
| WSL2 | 지원 사용 경로로만 안내, 이번 검증에서 실제 실행하지 않음 |
| Windows 네이티브 | 지원하지 않음 |

Linux 컨테이너 검증은 Docker Engine이 컨테이너 밖에 있다는 전제로 수행했습니다. 컨테이너 안에 Docker를
설치하거나 연결하지 않았습니다. 제품 전체 기능과 정식 릴리스는 이 티켓의 범위가 아닙니다.
