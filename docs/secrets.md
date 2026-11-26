# 비밀값 연결 (secrets)

dcraft는 비밀값 자체를 저장하지 않습니다. 서비스·시점·전달 방식·원본 위치만
`.dcraft.yaml`의 `secrets:` 항목에 저장하고 실행할 때 필요한 원본만 읽습니다.

## 지원 범위

- 시점: `runtime`(컨테이너 실행)과 `build`(BuildKit secret mount).
- 원본: `env`(환경변수), `file`(로컬 파일), `aws`(AWS Secrets Manager).
- 전달: runtime은 `environment`(컨테이너 환경변수) 또는 `file`
  (`/run/secrets/<target>`). build는 항상 `file`(BuildKit secret mount)입니다.

## 추가·조회·제거

```sh
# 환경변수 / 로컬 파일
dcraft secrets --add -s web --delivery environment --source env --ref DB_PASSWORD
dcraft secrets --add -s worker --delivery file --source file --ref secrets/token.txt

# AWS Secrets Manager (target 필수)
dcraft secrets --add -s web --delivery environment --source aws \
  --ref prod/db --field database.password --region ap-northeast-2 \
  --aws-profile ops-admin --target DB_PASSWORD
dcraft secrets --add -s builder --phase build --source aws \
  --ref build/token --field token --target BUILD_TOKEN

dcraft secrets
dcraft secrets --json
dcraft secrets --remove -s web --delivery environment --target DB_PASSWORD
```

- `--ref`에는 AWS secret 이름 또는 전체 ARN을 씁니다.
- `--field`는 JSON SecretString 안에서 전달할 **하나의 스칼라 값**(문자열·숫자·
  true/false)을 가리키는 dot 경로입니다. 키에 `.`이 들어 있으면 지원하지
  않습니다. `--field`를 생략하면 SecretString 전체를 전달합니다.
- `--region`/`--aws-profile`은 선택 사항입니다. 생략하면 AWS CLI 설정을
  사용합니다. `--profile`은 Compose profile 전용이라 AWS에는
  `--aws-profile`을 사용합니다.
- AWS 원본은 `--target`이 필수입니다. secret 이름/ARN에서 대상 이름을 자동으로
  만들지 않습니다.
- 바이너리 전용(SecretString 없음) 원본, 빈 값, 공백뿐인 값은 실패합니다.
  dcraft가 빈 값을 조용히 전달하지 않습니다.

대화형 화면(`dcraft`)에서는 서비스 → 시점 → 전달 방식 → 원본 종류 → 원본
참조 순서로 선택합니다. AWS를 고르면 전달 대상, field, region, profile 단계가
이어서 나타나며 field·region·profile은 비워 두고 Enter로 건너뛸 수 있습니다.
저장 전에 결과를 확인하고 취소하면 아무것도 저장하지 않습니다.

`.dcraft.yaml`은 팀과 공유할 수 있는 참조 설정입니다. 원본 이름·경로·대상은
드러나지만 값과 자격 증명은 저장되지 않습니다. 기존 Compose 파일과 Dockerfile은
dcraft가 수정하지 않습니다. `--add`는 참조만 저장하고 원본 존재 여부는 실행
시점에 확인합니다. 기존 Compose 서비스에 같은 이름의 `environment:` 항목이나
같은 `/run/secrets` 대상이 이미 있으면 충돌할 수 있으므로 대상을 확인하세요.

## AWS 인증과 조회

- dcraft는 설치된 AWS CLI를 실행하며 자격 증명은 기존 AWS CLI 인증 수단
  (환경변수, `~/.aws` 공유 설정, SSO, 인스턴스 역할 등)을 그대로 사용합니다.
  dcraft 설정·상태·생성 스크립트에 자격 증명을 넣지 않습니다.
- AWS CLI v2가 PATH에 있고 유효한 자격 증명과 리전이 준비되어 있어야 합니다.
  dcraft는 CLI를 자동 설치하거나 `aws configure`를 대신 실행하지 않습니다.
- 현재 작업이 실제 참조하는 항목만 조회합니다. 한 작업에서 같은 secret은
  region/profile 조합별로 한 번만 조회하고 조회한 값을 다음 작업을 위해
  저장하거나 비교하지 않습니다.
- `dcraft secrets --add`/목록 조회는 AWS를 호출하지 않습니다. `dcraft up`은
  실행 시점 참조를, `dcraft restart`는 파일 전달 참조만, `dcraft update`는
  빌드 시점 참조를 조회합니다.
- 자격 증명 없음, 리전 없음, 권한 거부(IAM/KMS), 원본 없음, field 불일치,
  네트워크 실패, 빈 값은 서로 다른 오류로 안내합니다. 오류에는 값이나 자격
  증명, 원본 stderr 전체를 출력하지 않습니다.
- 실제 AWS API 검증은 명시적으로 허용된 격리 자격 증명이 있을 때만 수행해야
  합니다. 이 버전의 자동 테스트는 통제된 `aws` 대체 실행 파일로 검증했습니다.

## 동작과 수명

- 환경변수 전달: `dcraft up`이 원본 값을 읽어 Compose 하위 프로세스 환경으로만
  전달합니다. Docker 조회 권한이 있는 사용자는 컨테이너 환경에서 값을 읽을 수
  있고 `docker compose config`는 서비스 `environment:` 값을 출력할 수 있습니다.
- 파일 전달: 값을 상태 폴더(`DCRAFT_STATE_DIR`)의 스택별 `secrets/`에 0600으로
  복사하고 `/run/secrets/<target>`에 읽기 전용으로 연결합니다. 앱이 이 경로를
  직접 읽어야 하며 dcraft가 앱의 파일 소비를 보장하지는 않습니다.
- 파일 전달 값은 컨테이너가 재시작될 수 있는 동안 유지됩니다. 참조를 제거해도
  파일은 즉시 지우지 않고 서비스를 다시 만들 수 있는 성공적인 `dcraft up` 또는
  `dcraft down` 뒤에 정리합니다. 그래야 제거 직후에도 실행 중인 컨테이너가
  재시작될 수 있습니다. 참조 제거 후 `dcraft up`을 실행하면 서비스가 파일 없이
  다시 만들어지고 이전 파일이 정리됩니다.
- `dcraft up`과 `restart`는 파일 전달 값을 매번 현재 원본에서 다시 씁니다.
  dcraft는 이전 값을 저장하거나 비교하지 않으므로 원본이 바뀌었는지 스스로
  판단하지 않습니다.
- 원본이 없거나 비어 있거나 읽을 수 없으면 서비스를 바꾸기 전에 실패합니다.
  dcraft가 읽은 값은 도구 출력·진단·JSON·이력에서 가려집니다(끝의 줄바꿈 변형
  포함). 앱이나 사용자 스크립트가 직접 출력하는 값은 dcraft가 통제하지
  않습니다. `dcraft up`의 실시간 스트리밍에서는 줄바꿈을 걸친 값이나 64KB
  줄 버퍼를 넘는 값이 화면에 즉시 가려지지 않을 수 있지만 캡처된 진단은
  같은 값도 항상 가려집니다.
- 빌드 시점 값은 BuildKit secret mount로만 전달합니다. Dockerfile이
  `RUN --mount=type=secret,id=<target>`로 소비해야 하며 dcraft는 Dockerfile을
  자동 수정하지 않습니다. 값 변경은 빌드 캐시를 무효화하지 않으므로 새 값은
  `dcraft update --no-cache`로 다시 빌드해야 합니다.
- 세션 밖에서 만든 진단(예: 백업 도우미 컨테이너 출력)은
  `Runtime.RedactText(target, text)`로 같은 참조 값을 가립니다. 이 도우미는
  참조된 env/file 원본만 읽고 값을 반환하거나 저장하지 않으며 없는 원본은
  건너뜁니다. 진단만으로 AWS 조회를 유발하지 않습니다. 가림은 최선 노력이며
  dcraft가 가져오지 않은 값까지 보장하지 않습니다.

## 기존 설정에서 옮기기

Compose 파일이나 `.env`에 직접 적은 민감값은 dcraft가 자동으로 찾아 제거하지
않습니다. 값을 외부 원본(환경변수, 파일, AWS Secrets Manager)으로 옮긴 뒤 위
명령으로 참조를 등록하세요. Docker 자체의 이미지·컨테이너 저장·조회 경계는
dcraft가 통제하지 않습니다.
