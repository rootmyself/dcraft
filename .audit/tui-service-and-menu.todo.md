# TUI 서비스 접속·메뉴 개편

## 1. 이슈 등록
- [ ] 부모 스펙 이슈
- [ ] 하위 4개 이슈

## 2. 메뉴 계약 (Menu 선언)
- [ ] core.MenuEntry/MenuKind + Descriptor.Menu
- [ ] Registry 검증 규칙
- [ ] NAV 빌더 + 그룹/들여쓰기 렌더
- [ ] 커서를 메뉴 항목 인덱스로
- [ ] environments 2항목, hooks 그룹 + gitgen/s3gen Parent
- [ ] EnvironmentAdmin 제거
- [ ] 카운터 '메뉴 · N' / 'N개 항목'

## 3. 서비스 접속 폼
- [ ] 필드 사이 빈 줄 1줄
- [ ] InputField.DependsOn/DependsValue
- [ ] command 필드 조건부 + Required
- [ ] 숨김 필드 미제출

## 4. 접속 화면
- [ ] ED2 → 화면+스크롤백 초기화 (메인 화면만)
- [ ] 헤더에 '종료: ctrl+]' 합침
- [ ] 단위 + e2e

## 5. 실행환경 저장·전환
- [ ] active_environment 최상위 키
- [ ] 엔진 SetActiveEnvironment seam
- [ ] 전환 목록 제목/동작/빈 목록/현재 선택
- [ ] 생성·제거 후 활성 저장 + 주 화면 재실행
- [ ] 시작 시 활성 값 사용 + 무시 안내

## 6. 마무리
- [ ] docs/architecture.md 갱신
- [ ] 골든 재생성 + 추가
- [ ] 전체 테스트
- [ ] PR
