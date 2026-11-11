# Запросы для генерации макетов

Семь макетов созданы встроенным ImageGen. К общей части запроса добавлялось описание соответствующего экрана.

## Общая часть

Use case: ui-mockup. Create ONE high fidelity desktop website screenshot for Russian gift-wishlist app “Вишлист.”, landscape 3:2 or 16:10. Flat front-on website viewport from edge to edge, no device, browser chrome, design presentation or external caption. Visual language of user's Ohmywishes references as described: pure #000 black canvas, white bold rounded modern sans (Cyrillic, like Manrope), #202020 rounded cards, #a2a2a2 muted supporting text, electric royal blue #123cff pill CTAs, a subtle violet icon accent, generous black whitespace and photo/3D media only when specified. No gradients in UI surfaces, shadows, outlines around every card, frosted glass, artificial textures, or wall of text. Keep typographic hierarchy, sophisticated compact product layout and clean crisp natural readable Russian text, spell exact labels. Shared logged-in app shell: white wordmark “Вишлист.” with blue dot in top-left, a narrow ~212px left sidebar on black with muted line icons and exactly “Мои списки”, “Брони и история”, “Профиль”, a wide content area inset 260px from left and 52px from right, a dark pill “Выйти” at top right. Highlight selected sidebar item in a charcoal pill. Keep functional navigation only. Product data throughout: Марина, “День рождения Марины”, 28 ноября 2026, gift examples “Наушники”, “Керамическая кружка”, “Настольная игра”.

## 01-auth

This is the logged-out authentication screen, so omit the logged-in shell except wordmark “Вишлист.” in top left and use the full canvas. Two-column black composition. Left: oversized headline “Подарки, которые ждут”, one short gray line “Соберите список желаний и поделитесь им с друзьями.”, one playful dimensional cobalt blue gift box with saturated golden ribbon, sculptural reference motif, ample air. Right: one 420px charcoal rounded auth card with large “Войти”, pill switch “Вход” and “Регистрация” with Вход active, two dark inset fields labeled “Email”, “Пароль”, and one full-width blue “Войти” button. No additional account methods or marketing navigation. A design with clear purposeful content and precise Cyrillic.

## 02-my-lists

Logged-in shell, “Мои списки” active. Main header “Мои списки”, short secondary text “Списки к вашим праздникам” and on the right cobalt “+ Создать список”. Under it a compact pill switch “Активные” selected, “Архив”. Display exactly three large rounded charcoal list tiles side by side: “День рождения Марины” with “28 ноября 2026”, “Новый год” with “31 декабря 2026”, and “Новоселье” with “23 января 2027”. On each, a small pill “Активный”, heading and date below a playful isolated 3D pictogram (respectively blue gift with golden ribbon, purple bauble, orange house). Functional product dashboard with lively images and negative space below the row, no charts, no bogus metadata or features.

## 03-owner-list

Logged-in shell, “Мои списки” active. The wishlist owner's active page. Small “← Мои списки” and pill “Активный” above prominent “День рождения Марины”, date “28 ноября 2026 · Москва”. Hero with small blue gift-box pictogram as atmospheric accent, compact buttons “Поделиться”, “Настройки”, and a prominent cobalt “+ Добавить подарок”. Under header “Подарки”, a gallery row of three consistently aligned rounded cards with a refined dark art tile for each generic conceptual illustration, depicting cobalt headphones, a sand ceramic mug and purple board game pieces. Dark plates with concise titles “Наушники”, “Керамическая кружка”, “Настольная игра”, small ellipsis menu on each for editing. Put action row and cards on the same viewport. Imagery should feel like coherent decorative 3D placeholders in the absence of product photos. No reservation icons, labels, identities, prices, or status on any card, because the owner's surprises are private.

## 04-shared-list

Logged-in shell with neutral sidebar navigation, a guest arriving through a shared URL. The reference's public profile composition inspires a centered header above the wide catalog: circular stylized initials avatar “М”, large “День рождения Марины”, below “Марина · 28 ноября 2026” and muted sentence “Выберите подарок и забронируйте его.”. Three equal gallery cards under “Подарки” use dark art tiles with isolated sculptural 3D placeholder illustrations, cobalt headphones, sand ceramic mug, purple board game pieces. Card titles left to right exactly “Наушники”, “Керамическая кружка”, “Настольная игра”. Below the first show primary blue “Забронировать”. Below second show muted “Занят” with no action. Below third show “Ваша бронь” and a small dark “Отменить бронь” action. No prices, profile metrics, friends, store badges or owners' editing buttons. Make the three states clearly distinct.

Правка результата

Use case: precise-object-edit. The only input image is the edit target. This is a pixel-faithful edit of the entire existing website screenshot, with identical 1536x1024 framing. Make only the first left navigation item “Мои списки” look inactive, because the user is viewing a friend's list. Remove the charcoal rounded rectangular highlight behind it, returning that rectangle to the same solid black as the rest of the sidebar. Change only that row's gift outline icon and text to match the muted gray of the other two rows. Preserve positions, exact legible Cyrillic, the other nav items, the rest of the UI, all card actions, imagery, typography, and colors everywhere else. No elements move.

## 05-archived-list

Logged-in shell, “Мои списки” active. Owner's archived-list variant. Main heading “День рождения Марины”, pill “В архиве”, date “15 августа 2026”, “← Мои списки” breadcrumb. Section “Итоги” with a subtle gray line “Здесь можно исправить итог каждого подарка.” and cobalt button “Перенести неподаренные”. Three charcoal gallery cards with coherent dark 3D placeholders for cobalt headphones, sand ceramic mug and purple board game pieces, same visual style as the active list. Titles “Наушники”, “Керамическая кружка”, “Настольная игра”. Give each one compact dropdown chip with chevron indicating changeable result: first and third “Подарено” in restrained sage green, middle “Не подарено” in gray. No booking buttons, live countdown, adding gifts, or identities of reservers. Make archive visibly distinct yet the same website.

Правка результата

Use case: text-localization. The only input image is the edit target. Maintain the full existing 1536x1024 website screenshot, styling, proportions, placement, art and typography exactly. Make exactly two small corrections to the Russian text labels: the date underneath the heading should read “28 ноября 2025”, and the primary blue button should read “Скопировать неподаренные”. Preserve the small calendar icon, the shape and color of the blue button, all other words, gift images and layouts without alteration. Crisp correct Cyrillic.

## 06-bookings-history

Logged-in shell, “Брони и история” active. Main heading “Брони и история”, two large pill tabs “Текущие брони” active and “История подарков” secondary. A simple charcoal list panel, three comfortable readable rows, small square stylized product illustrations, gift title, recipient and list subtitle. Row 1 “Настольная игра” with “Марина · День рождения Марины” and a dark pill action “Отменить бронь”. Row 2 “Чайный набор” with “Саша · Новоселье” and “Отменить бронь”. Row 3 “Книга” with “Лена · День рождения” and muted badge “Отмена закрыта”. Keep natural label alignment and readable large crisp Russian. Add no analytics, prices, follower stats, or other headings. Whitespace underneath.

## 07-profile

Logged-in shell, “Профиль” active. Main centered medium-width charcoal form on black, visual focus on typography and generous whitespace. Main heading “Профиль”. A 64px circular violet-tinted initials avatar “М”, display name “Марина”, gray “marina@example.com” as read-only context. Three clean form fields “Размер одежды” value “M”, “Размер обуви (EU)” value “38”, “Дата рождения” value “28.11.1998”. Simple muted privacy hint “Размеры и дата рождения видны только вам.” and blue pill “Сохранить”. No account sections, passwords, notification toggles, follower counts or upload controls. Closely aligned airy settings layout, appearance belongs to the same dark app.
