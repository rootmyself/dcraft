# 비밀값 연결 (runtime secrets)

dcraft는 비밀값 자체를 저장하지 않습니다. 서비스·시점·전달 방식·원본 위치만
`.dcraft.yaml`의 `secrets:` 항목에 저장하고, 실행할 때 필요한 원본만 읽습니다.

## 지원 범위

- 시점: `runtime`만 지원합니다. `build`는 인식하지만 아직 구현되지 않았습니다.
- 원본: `env`(환경변수), `file`(로컬 파일)만 지원합니다. `aws`는 인식하지만
  아직 구현되지 않았습니다.
- 전달: `environment`(컨테이너 환경변수), `file`(`/run/secrets/<target>` 파일).

## 추가·조회·제거

```sh
dcraft secrets --add -s web --delivery environment --source env --ref DB_PASSWORD
dcraft secrets --add -s worker --delivery file --source file --ref secrets/token.txt
dcraft secrets
dcraft secrets --json
dcraft secrets --remove -s web --delivery environment --target DB_PASSWORD
```

대화형 화면(`dcraft`)에서는 서비스 → 시점 → 전달 방식 → 원본 종류 → 원본
참조 순서로 선택하고, 저장 전에 결과를 확인합니다. 취소하면 아무것도 저장하지
않습니다.

`.dcraft.yaml`은 팀과 공유할 수 있는 참조 설정입니다. 원본 이름·경로·대상은
드러나지만 값은 저장되지 않습니다. 기존 Compose 파일과 Dockerfile은 dcraft가
수정하지 않습니다. `--add`는 참조만 저장하고 원본 존재 여부는 실행 시점에
확인합니다. 기존 Compose 서비스에 같은 이름의 `environment:` 항목이나 같은
`/run/secrets` 대상이 이미 있으면 충돌할 수 있으므로 대상을 확인하세요.

## 동작과 수명

- 환경변수 전달: `dcraft up`이 원본 값을 읽어 Compose 하위 프로세스 환경으로만
  전달합니다. Docker 조회 권한이 있는 사용자는 컨테이너 환경에서 값을 읽을 수
  있고, `docker compose config`는 서비스 `environment:` 값을 출력할 수 있습니다.
- 파일 전달: 값을 상태 폴더(`DCRAFT_STATE_DIR`)의 스택별 `secrets/`에 0600으로
  복사하고 `/run/secrets/<target>`에 읽기 전용으로 연결합니다. 앱이 이 경로를
  직접 읽어야 하며, dcraft가 앱의 파일 소비를 보장하지는 않습니다.
- 파일 전달 값은 컨테이너가 재시작될 수 있는 동안 유지됩니다. 참조를 제거해도
  파일은 즉시 지우지 않고, 서비스를 다시 만들 수 있는 성공적인 `dcraft up` 또는
  `dcraft down` 뒤에 정리합니다. 그래야 제거 직후에도 실행 중인 컨테이너가
  재시작될 수 있습니다. 참조 제거 후 `dcraft up`을 실행하면 서비스가 파일 없이
  다시 만들어지고 이전 파일이 정리됩니다.
- `dcraft up`과 `restart`는 파일 전달 값을 매번 현재 원본에서 다시 씁니다.
  dcraft는 이전 값을 저장하거나 비교하지 않으므로, 원본이 바뀌었는지 스스로
  판단하지 않습니다.
- 원본이 없거나 비어 있거나 읽을 수 없으면 서비스를 바꾸기 전에 실패합니다.
  dcraft가 읽은 값은 도구 출력·진단·JSON·이력에서 가려집니다. 앱이나 사용자
  스크립트가 직접 출력하는 값은 dcraft가 통제하지 않습니다.

## 기존 설정에서 옮기기

Compose 파일이나 `.env`에 직접 적은 민감값은 dcraft가 자동으로 찾아 제거하지
않습니다. 값을 외부 원본(환경변수 또는 파일)으로 옮긴 뒤 위 명령으로 참조를
등록하세요. Docker 자체의 이미지·컨테이너 저장·조회 경계는 dcraft가 통제하지
않습니다.
