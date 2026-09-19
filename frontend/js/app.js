// Menu do celular: abre e fecha a sidebar como gaveta, com o fundo
// escurecido atrás. Fecha no ESC, ao tocar num link ou quando a tela fica
// larga o bastante para a sidebar ficar fixa (768px, o mesmo número do CSS).
function initMobileNavigation() {
    const menuButton = document.querySelector('.mobile-menu-button');
    const sidebar = document.querySelector('.sidebar');
    const overlay = document.querySelector('.mobile-sidebar-overlay');
    if (!menuButton || !sidebar || !overlay) return;

    const setOpen = function (open) {
        document.body.classList.toggle('mobile-menu-open', open);
        menuButton.setAttribute('aria-expanded', open ? 'true' : 'false');
        menuButton.setAttribute('aria-label', open ? 'Fechar menu' : 'Abrir menu');
    };

    menuButton.addEventListener('click', function () {
        setOpen(!document.body.classList.contains('mobile-menu-open'));
    });

    overlay.addEventListener('click', function () {
        setOpen(false);
    });

    sidebar.querySelectorAll('a').forEach(function (link) {
        link.addEventListener('click', function () {
            setOpen(false);
        });
    });

    document.addEventListener('keydown', function (event) {
        if (event.key === 'Escape') setOpen(false);
    });

    window.addEventListener('resize', function () {
        if (window.innerWidth >= 768) setOpen(false);
    });
}

// No celular cada linha de tabela vira um cartão (ver layout.css), e cada
// célula precisa mostrar o nome da coluna. Aqui o texto de cada <th> é
// copiado para o data-label das células daquela coluna.
function initResponsiveTableLabels() {
    document.querySelectorAll('.content table').forEach(function (table) {
        const headers = Array.from(table.querySelectorAll('thead th')).map(function (th) {
            return th.textContent.trim();
        });
        if (!headers.length) return;

        table.querySelectorAll('tbody tr').forEach(function (row) {
            Array.from(row.children).forEach(function (cell, index) {
                if (cell.hasAttribute('colspan')) return;
                if (headers[index]) cell.setAttribute('data-label', headers[index]);
            });
        });
    });
}

// Linhas de item do formulário de nova solicitação (/solicitacoes/nova):
// acrescentar, remover e mostrar a unidade do material escolhido. O JS faz
// só isso. Toda validação (item repetido, quantidade, limite de itens)
// fica no servidor.
function enableRequestItemRows() {
    const container = document.querySelector("[data-request-items]");
    const template = document.getElementById("request-item-template");
    if (!container || !template) return;

    const max = parseInt(container.dataset.maxItems, 10) || 30;
    const addButton = document.querySelector("[data-add-item]");
    const rows = function () {
        return container.querySelectorAll("[data-request-item]");
    };

    const syncUnit = function (row) {
        const select = row.querySelector("select");
        const unit = row.querySelector("[data-item-unit]");
        const option = select ? select.options[select.selectedIndex] : null;
        if (unit) unit.textContent = option && option.dataset.unit ? option.dataset.unit : "";
    };

    // Não deixa passar do limite de itens nem remover a última linha.
    const syncButtons = function () {
        const current = rows();
        if (addButton) addButton.disabled = current.length >= max;
        current.forEach(function (row) {
            const remove = row.querySelector("[data-remove-item]");
            if (remove) remove.disabled = current.length === 1;
        });
    };

    if (addButton) {
        addButton.addEventListener("click", function () {
            if (rows().length >= max) return;
            const row = template.content.firstElementChild.cloneNode(true);
            container.appendChild(row);
            syncUnit(row);
            syncButtons();
            row.querySelector("select").focus();
        });
    }

    // O clique é ouvido no container, e não em cada linha, porque as linhas
    // criadas depois não teriam o ouvinte.
    container.addEventListener("click", function (event) {
        const remove = event.target.closest("[data-remove-item]");
        if (!remove || rows().length <= 1) return;
        remove.closest("[data-request-item]").remove();
        syncButtons();
    });
    container.addEventListener("change", function (event) {
        const row = event.target.closest("[data-request-item]");
        if (row) syncUnit(row);
    });

    rows().forEach(syncUnit);
    syncButtons();
}

// Mantém o ano do rodapé certo. O HTML já vem com o ano escrito, para o
// rodapé ficar completo mesmo sem JS; isto só corrige na virada do ano,
// para ninguém precisar editar o template todo 1º de janeiro.
function initCurrentYear() {
    const ano = String(new Date().getFullYear());
    document.querySelectorAll("[data-current-year]").forEach(function (el) {
        if (el.textContent.trim() !== ano) el.textContent = ano;
    });
}

document.addEventListener("DOMContentLoaded", function () {
    initMobileNavigation();
    initResponsiveTableLabels();
    initCurrentYear();
    initLoginLockCountdown();
    enableRequestItemRows();
    const password = document.getElementById("password");
    const togglePassword = document.getElementById("togglePassword");
    const eyeIcon = document.getElementById("eyeIcon");

    if (password && togglePassword && eyeIcon) {
        togglePassword.addEventListener("click", function () {
            const passwordVisible = password.type === "text";

            password.type = passwordVisible ? "password" : "text";

            if (passwordVisible) {
                eyeIcon.innerHTML = `
                    <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"></path>
                    <circle cx="12" cy="12" r="3"></circle>
                `;
                togglePassword.setAttribute("aria-label", "Mostrar senha");
                return;
            }

            eyeIcon.innerHTML = `
                <path d="M3 3l18 18"></path>
                <path d="M10.6 10.6a2 2 0 0 0 2.8 2.8"></path>
                <path d="M9.9 4.2A10.8 10.8 0 0 1 12 4c6.5 0 10 8 10 8a17.2 17.2 0 0 1-3.1 4.2"></path>
                <path d="M6.6 6.6C3.8 8.5 2 12 2 12s3.5 8 10 8a9.8 9.8 0 0 0 4.2-.9"></path>
            `;
            togglePassword.setAttribute("aria-label", "Ocultar senha");
        });
    }

    // Modal de cadastro de material. Dois botões abrem o mesmo modal: o do
    // cabeçalho da lista e o que aparece quando a tabela está vazia.
    //
    // O clique é ouvido no document, e não em cada botão (isso se chama
    // delegação). O botão da tabela vazia fica dentro da tabela, que a busca
    // em tempo real substitui por uma nova; um ouvinte preso no botão antigo
    // sumiria junto com ele.
    const createModal = document.getElementById("create-material-modal");
    const closeCreateModal = document.getElementById("close-create-material");

    if (createModal && closeCreateModal) {
        const closeModal = function () {
            createModal.hidden = true;
        };

        document.addEventListener("click", function (event) {
            if (!event.target.closest("#open-create-material, [data-open='create-material-modal']")) return;
            createModal.hidden = false;
            createModal.querySelector("input")?.focus();
        });

        closeCreateModal.addEventListener("click", closeModal);

        createModal.addEventListener("click", function (event) {
            if (event.target === createModal) {
                closeModal();
            }
        });

        document.addEventListener("keydown", function (event) {
            if (event.key === "Escape" && !createModal.hidden) {
                closeModal();
            }
        });
    }

    initAnimations();
});

// Liga os comportamentos que valem em todas as telas e as animações.
//
// O visual das animações fica no components.css (classes gs-*). Aqui o JS
// só coloca e tira essas classes. Quem pediu menos animação no sistema
// operacional fica só com o que é funcional: busca, confirmação, toasts.
function initAnimations() {
    enableSearchableSelects();
    enableLiveSearch();
    enableAutoSubmitSelects();
    enableFormLoadingState();
    enableDeleteConfirmation();
    enableToasts();
    enableSearchHighlightFromUrl();

    if (window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        return;
    }

    enableClickRipple();
    enablePageTransition();
    animateContentEntrance();
    animateRowsCascade();
    animateCards();
    animateCounters();
    animateModalClosing();
    animateLoginError();
}

// Tira os acentos e deixa tudo minúsculo, para a busca achar "Maceió"
// quando alguém digita "maceio". O normalize("NFD") separa a letra do
// acento ("ó" vira "o" + "´") e o replace apaga os acentos que sobraram.
function foldText(text) {
    return text.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

// Lista com busca para <select data-searchable>, usada no seletor de obra
// do topo e no campo Obra dos usuários.
//
// O <select> original continua no formulário, só que escondido: é ele que
// guarda o valor e vai para o servidor. Na frente dele fica um botão que
// abre um painel com um campo de busca e a lista filtrada. Sem JavaScript,
// o <select> comum aparece e funciona normalmente.
//
// No teclado, as setas percorrem a lista, Enter escolhe e Esc fecha. Ao
// escolher, o <select> recebe o valor e dispara "change", e é assim que o
// seletor de obra continua enviando o formulário sozinho.
function enableSearchableSelects() {
    document.querySelectorAll("select[data-searchable]").forEach(function (select, index) {
        const wrapper = document.createElement("div");
        wrapper.className = "gs-combobox";

        const button = document.createElement("button");
        button.type = "button";
        button.className = "gs-combobox-button";
        button.id = (select.id || "gs-combobox-" + index) + "-botao";
        button.setAttribute("aria-haspopup", "listbox");
        button.setAttribute("aria-expanded", "false");

        const panel = document.createElement("div");
        panel.className = "gs-combobox-panel";
        panel.hidden = true;

        const listId = button.id + "-lista";
        const input = document.createElement("input");
        input.type = "search";
        input.className = "gs-combobox-search";
        input.placeholder = select.dataset.searchPlaceholder || "Buscar...";
        input.setAttribute("aria-label", input.placeholder);
        input.setAttribute("aria-controls", listId);
        input.autocomplete = "off";

        const list = document.createElement("ul");
        list.className = "gs-combobox-list";
        list.id = listId;
        list.setAttribute("role", "listbox");

        const empty = document.createElement("p");
        empty.className = "gs-combobox-empty";
        empty.textContent = "Nada encontrado.";
        empty.hidden = true;

        panel.append(input, list, empty);
        wrapper.append(button, panel);
        select.after(wrapper);
        select.classList.add("gs-combobox-native");

        // O <label for="..."> que apontava para o select passa a apontar
        // para o botão.
        if (select.id) {
            document.querySelectorAll('label[for="' + select.id + '"]').forEach(function (label) {
                label.htmlFor = button.id;
            });
        }

        let items = [];
        let active = -1;

        const syncLabel = function () {
            const option = select.options[select.selectedIndex];
            button.textContent = option ? option.text : "Selecionar";
        };

        const setActive = function (position) {
            items.forEach(function (item, i) {
                item.classList.toggle("gs-combobox-active", i === position);
            });
            active = position;
            if (items[position]) {
                input.setAttribute("aria-activedescendant", items[position].id);
                items[position].scrollIntoView({ block: "nearest" });
            } else {
                input.removeAttribute("aria-activedescendant");
            }
        };

        const render = function () {
            const term = foldText(input.value.trim());
            list.innerHTML = "";
            items = [];
            Array.from(select.options).forEach(function (option, i) {
                if (term && !foldText(option.text).includes(term)) return;
                const item = document.createElement("li");
                item.id = listId + "-" + i;
                item.setAttribute("role", "option");
                item.setAttribute("aria-selected", option.selected ? "true" : "false");
                item.dataset.value = option.value;
                item.textContent = option.text;
                // O preventDefault no mousedown mantém o foco no campo de
                // busca. Sem isso o painel fecharia antes do clique contar.
                item.addEventListener("mousedown", function (event) {
                    event.preventDefault();
                });
                item.addEventListener("click", function () {
                    choose(option.value);
                });
                list.append(item);
                items.push(item);
            });
            empty.hidden = items.length > 0;
            const selected = items.findIndex(function (item) {
                return item.dataset.value === select.value;
            });
            setActive(selected >= 0 ? selected : (items.length ? 0 : -1));
        };

        const open = function () {
            panel.hidden = false;
            button.setAttribute("aria-expanded", "true");
            input.value = "";
            render();
            input.focus();
        };

        const close = function (returnFocus) {
            if (panel.hidden) return;
            panel.hidden = true;
            button.setAttribute("aria-expanded", "false");
            if (returnFocus) button.focus();
        };

        const choose = function (value) {
            const changed = select.value !== value;
            select.value = value;
            syncLabel();
            close(true);
            if (changed) select.dispatchEvent(new Event("change", { bubbles: true }));
        };

        button.addEventListener("click", function () {
            if (panel.hidden) {
                open();
            } else {
                close(false);
            }
        });
        button.addEventListener("keydown", function (event) {
            if (event.key === "ArrowDown") {
                event.preventDefault();
                open();
            }
        });

        input.addEventListener("input", render);
        input.addEventListener("keydown", function (event) {
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                if (!items.length) return;
                const step = event.key === "ArrowDown" ? 1 : -1;
                setActive((active + step + items.length) % items.length);
            } else if (event.key === "Enter") {
                // Aqui o Enter escolhe a opção, e não pode enviar o
                // formulário.
                event.preventDefault();
                if (items[active]) choose(items[active].dataset.value);
            } else if (event.key === "Escape") {
                // O stopPropagation faz o Esc fechar só a lista, e não o
                // modal em volta dela.
                event.preventDefault();
                event.stopPropagation();
                close(true);
            } else if (event.key === "Tab") {
                close(false);
            }
        });

        document.addEventListener("click", function (event) {
            if (!wrapper.contains(event.target)) close(false);
        });

        // Quando outro script muda o valor (o modal de editar usuário
        // preenche a obra, por exemplo), ele dispara "change" e o texto do
        // botão acompanha.
        select.addEventListener("change", syncLabel);
        syncLabel();
    });
}

// Busca em tempo real, para todo <form data-live-search> (os filtros de
// obras, materiais, estoque, movimentações e usuários).
//
// Enquanto a pessoa digita, o JS pede ao servidor a mesma página que o
// botão "Filtrar" abriria e troca só os pedaços marcados com
// data-live-region (tabela, paginação, contadores). O campo de busca não é
// trocado, então o foco e o cursor ficam onde estavam. Sem JavaScript, o
// formulário funciona do jeito normal, pelo botão.
//
// Dois cuidados:
// - espera 300 ms sem digitar antes de pedir, para não fazer um pedido por
//   letra;
// - o AbortController cancela o pedido anterior que ainda não voltou. Sem
//   isso, uma resposta lenta de "ci" poderia chegar depois da de "cimento"
//   e mostrar o resultado errado.
function enableLiveSearch() {
    document.querySelectorAll("form[data-live-search]").forEach(function (form) {
        let timer = null;
        let controller = null;

        const search = function () {
            const params = new URLSearchParams(new FormData(form));
            // Campo vazio não vai para a URL: "?busca=&ordem=nome" vira
            // "?ordem=nome".
            Array.from(params.keys()).forEach(function (key) {
                if (!params.get(key).trim()) params.delete(key);
            });
            const query = params.toString();
            const url = form.getAttribute("action") + (query ? "?" + query : "");

            if (controller) controller.abort();
            controller = new AbortController();
            form.setAttribute("aria-busy", "true");

            fetch(url, { signal: controller.signal })
                .then(function (response) { return response.text(); })
                .then(function (html) {
                    const fresh = new DOMParser().parseFromString(html, "text/html");
                    const regions = document.querySelectorAll("[data-live-region]");
                    const replacements = Array.from(regions).map(function (region) {
                        return fresh.querySelector('[data-live-region="' + region.dataset.liveRegion + '"]');
                    });

                    // Se a resposta não tem as regiões esperadas (a sessão
                    // expirou e voltou a tela de login, por exemplo), abre a
                    // página inteira.
                    if (replacements.some(function (item) { return !item; })) {
                        window.location.href = url;
                        return;
                    }

                    regions.forEach(function (region, index) {
                        region.replaceWith(document.importNode(replacements[index], true));
                    });
                    // A URL acompanha a busca, então um F5 ou um link
                    // compartilhado mantém o filtro.
                    history.replaceState(null, "", url);

                    initResponsiveTableLabels();
                    const term = form.querySelector('input[type="search"]');
                    if (term && term.value.trim()) {
                        document.querySelectorAll("[data-live-region] tbody td").forEach(function (cell) {
                            highlightSearch(cell, term.value);
                        });
                    }
                })
                .catch(function (error) {
                    if (error.name === "AbortError") return;
                    window.location.href = url;
                })
                .finally(function () {
                    form.removeAttribute("aria-busy");
                });
        };

        // O "input" dispara a cada letra e também quando muda um <select> ou
        // uma data. Nesses dois casos a busca é imediata, porque não tem
        // digitação para esperar.
        form.addEventListener("input", function (event) {
            clearTimeout(timer);
            const typing = event.target.matches('input[type="search"], input[type="text"]');
            timer = setTimeout(search, typing ? 300 : 0);
        });

        // O Enter e o botão "Filtrar" também buscam sem recarregar a página.
        form.addEventListener("submit", function (event) {
            event.preventDefault();
            clearTimeout(timer);
            search();
        });
    });
}

// Envia o formulário assim que muda a opção de um <select data-autosubmit>,
// que é o seletor de obra do topo. Sem JS, o botão "Trocar" do <noscript>
// faz esse papel. Uso requestSubmit, e não submit, porque ele dispara o
// evento de envio normal, e o spinner de carregando continua funcionando.
function enableAutoSubmitSelects() {
    document.querySelectorAll("select[data-autosubmit]").forEach(function (select) {
        select.addEventListener("change", function () {
            if (select.form.requestSubmit) {
                select.form.requestSubmit();
            } else {
                select.form.submit();
            }
        });
    });
}

// Faz o bloco principal da página aparecer suavemente (o conteúdo das
// telas internas, a tela de acesso negado ou o formulário de login).
function animateContentEntrance() {
    const target = document.querySelector(".content, .access-denied-card, .login-form-side");
    if (!target) return;

    target.classList.add("gs-enter");
    requestAnimationFrame(function () {
        requestAnimationFrame(function () {
            target.classList.add("gs-visible");
        });
    });
}

// Faz as linhas de tabela aparecerem uma logo depois da outra.
function animateRowsCascade() {
    document.querySelectorAll(".content table tbody tr").forEach(function (row, index) {
        row.style.setProperty("--gs-i", index);
        row.classList.add("gs-row");
    });
}

// O mesmo efeito, nos cartões do dashboard.
function animateCards() {
    document.querySelectorAll(".cards .card").forEach(function (card, index) {
        card.style.setProperty("--gs-i", index);
        card.classList.add("gs-row");
    });
}

// Anima a saída dos modais antes de escondê-los de verdade. O
// MutationObserver percebe quando alguém põe o atributo hidden no modal,
// tira de novo por um instante para a animação rodar e só então esconde.
// Assim nenhum modal precisa de código próprio para fechar animado.
function animateModalClosing() {
    document.querySelectorAll(".modal-create, .modal-edit").forEach(function (modal) {
        let animating = false;
        let ignoreNextMutation = false;

        const observer = new MutationObserver(function () {
            if (ignoreNextMutation) {
                ignoreNextMutation = false;
                return;
            }
            if (!modal.hidden || animating) return;

            animating = true;
            ignoreNextMutation = true;
            modal.hidden = false;
            modal.classList.add("modal-closing");

            const finish = function (event) {
                if (event.target !== modal) return;
                modal.removeEventListener("animationend", finish);
                modal.classList.remove("modal-closing");
                animating = false;
                ignoreNextMutation = true;
                modal.hidden = true;
            };

            modal.addEventListener("animationend", finish);
        });

        observer.observe(modal, { attributes: true, attributeFilter: ["hidden"] });
    });
}

// Efeito de onda ao clicar em botões e links de ação. O clique é ouvido no
// document, então funciona em qualquer botão, inclusive nos que são
// criados depois (dentro de modais, por exemplo).
function enableClickRipple() {
    document.addEventListener("click", function (event) {
        const target = event.target.closest(
            "button, .new-material-button, .save-button, .edit-material-button, .sidebar-logout, .pagination a"
        );
        if (!target || target.disabled) return;

        const rect = target.getBoundingClientRect();
        const size = Math.max(rect.width, rect.height);
        const ripple = document.createElement("span");
        ripple.className = "gs-ripple";
        ripple.style.width = ripple.style.height = size + "px";
        ripple.style.left = (event.clientX - rect.left - size / 2) + "px";
        ripple.style.top = (event.clientY - rect.top - size / 2) + "px";

        if (getComputedStyle(target).position === "static") {
            target.style.position = "relative";
        }
        target.style.overflow = "hidden";
        target.appendChild(ripple);
        ripple.addEventListener("animationend", function () { ripple.remove(); });
    });
}

// Nos cartões do dashboard, os números sobem de 0 até o valor real em vez
// de já aparecerem prontos.
function animateCounters() {
    document.querySelectorAll(".card strong").forEach(function (el) {
        // O cartão com mais de um número fica de fora. O "Hoje" mostra
        // entradas e saídas em <span>s separados, e reescrever o texto dele
        // apagaria os spans, sobrando só o primeiro número.
        if (el.children.length) return;

        const originalText = el.textContent.trim();
        const prefix = (originalText.match(/^[^\d]*/) || [""])[0];
        const numberText = originalText.slice(prefix.length).trim();
        const finalValue = parseFloat(numberText.replace(/\./g, "").replace(",", "."));
        if (isNaN(finalValue)) return;

        const decimalPlaces = numberText.includes(",") ? 2 : 0;
        const duration = 900;
        const start = performance.now();

        function format(value) {
            return prefix + value.toLocaleString("pt-BR", {
                minimumFractionDigits: decimalPlaces,
                maximumFractionDigits: decimalPlaces,
            });
        }

        function step(now) {
            const progress = Math.min((now - start) / duration, 1);
            const eased = 1 - Math.pow(1 - progress, 3);
            el.textContent = format(finalValue * eased);
            if (progress < 1) requestAnimationFrame(step);
        }

        el.textContent = format(0);
        requestAnimationFrame(step);
    });
}

// Ao ir para outra tela pelo menu, pela paginação ou pelo "Sair", a página
// some suavemente antes de trocar, em vez de mudar de uma vez.
function enablePageTransition() {
    const linkSelectors = ".sidebar nav a, .pagination a, .sidebar-logout, .sidebar-account";

    document.addEventListener("click", function (event) {
        const link = event.target.closest(linkSelectors);
        if (!link) return;
        if (event.defaultPrevented || event.button !== 0) return;
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        if (link.target === "_blank") return;

        event.preventDefault();
        document.body.classList.add("gs-leaving");
        setTimeout(function () {
            window.location.href = link.href;
        }, 160);
    });
}

// Mostra um spinner no botão enquanto o formulário é enviado. Além de dar
// retorno, evita que alguém clique duas vezes numa ação mais demorada.
function enableFormLoadingState() {
    document.addEventListener("submit", function (event) {
        if (event.defaultPrevented) return;

        const form = event.target;
        if (!(form instanceof HTMLFormElement)) return;

        const button = form.querySelector('button[type="submit"], button:not([type])');
        if (button) {
            button.classList.add("gs-loading");
        }
    });
}

// Balança o card de login quando a página volta com erro de senha. A
// classe não é removida depois de propósito: tirar a classe faria a
// animação de entrada do card rodar de novo, porque o navegador reinicia a
// animação quando a propriedade "animation" volta ao valor anterior.
function animateLoginError() {
    const alert = document.querySelector(".login-alert");
    const card = document.querySelector(".login-card");
    if (!alert || !card) return;

    setTimeout(function () {
        card.classList.add("gs-shake");
    }, 900);
}

// Contagem regressiva do login bloqueado depois de muitas senhas erradas.
// Quem recusa as tentativas de verdade é o servidor; isto só mostra quanto
// falta e desabilita o botão, para a pessoa não insistir à toa. Sem
// JavaScript o servidor continua recusando, só não aparece o contador.
function initLoginLockCountdown() {
    const alert = document.querySelector(".login-alert[data-login-lock]");
    if (!alert) return;

    const output = alert.querySelector("[data-login-countdown]");
    const submit = document.querySelector(".login-card form button[type=\"submit\"]");
    let remaining = parseInt(alert.dataset.loginLock, 10);
    if (!output || !submit || !(remaining > 0)) return;

    submit.disabled = true;

    const tick = setInterval(function () {
        remaining -= 1;

        if (remaining > 0) {
            output.textContent = remaining;
            return;
        }

        clearInterval(tick);
        submit.disabled = false;
        alert.remove();
    }, 1000);
}

// Modal de confirmação no lugar do confirm() do navegador, que não dá para
// estilizar. É um modal só, criado uma vez e usado por qualquer formulário
// que tenha um botão com data-confirm, em qualquer tela.
//
// Por padrão o texto fala em remoção. O botão pode trocar o título
// (data-confirm-title), o texto do botão de confirmar (data-confirm-ok) e
// o tom (data-confirm-tone="warning"). Um botão com data-confirm-optional
// não pede confirmação, mas o script da tela pode colocar o data-confirm
// nele depois. É o que a tela de obras faz: só confirma quando a situação
// muda para paralisada ou concluída.
function enableDeleteConfirmation() {
    if (!document.querySelector("form [data-confirm], form [data-confirm-optional]")) return;

    const trashIcon = '<svg viewBox="0 0 24 24"><path d="M3 6h18"></path>' +
        '<path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>' +
        '<path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"></path></svg>';
    const warningIcon = '<svg viewBox="0 0 24 24"><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"></path>' +
        '<path d="M12 9v4M12 17h.01"></path></svg>';

    let modal = document.getElementById("gs-confirm-modal");
    if (!modal) {
        modal = document.createElement("div");
        modal.id = "gs-confirm-modal";
        modal.className = "modal-create";
        modal.hidden = true;
        modal.innerHTML =
            '<div class="modal-confirm-content" role="alertdialog" aria-modal="true" ' +
            'aria-labelledby="gs-confirm-title" aria-describedby="gs-confirm-text">' +
            '<div class="modal-confirm-icon" aria-hidden="true">' + trashIcon + "</div>" +
            '<h3 id="gs-confirm-title">Confirmar remoção</h3>' +
            '<p id="gs-confirm-text"></p>' +
            '<div class="modal-confirm-actions">' +
            '<button type="button" class="cancel-delete-button" id="gs-confirm-cancel">Cancelar</button>' +
            '<button type="button" class="confirm-delete-button" id="gs-confirm-ok">Remover</button>' +
            "</div></div>";
        document.body.appendChild(modal);
    }

    const titleEl = modal.querySelector("#gs-confirm-title");
    const iconEl = modal.querySelector(".modal-confirm-icon");
    const textEl = modal.querySelector("#gs-confirm-text");
    const confirmButton = modal.querySelector("#gs-confirm-ok");
    const cancelButton = modal.querySelector("#gs-confirm-cancel");
    let pendingForm = null;

    const close = function () {
        modal.hidden = true;
        // O enableFormLoadingState já colocou o spinner no botão antes de
        // este modal segurar o envio. Sem tirar o spinner aqui, o botão
        // ficaria travado depois do Cancelar.
        if (pendingForm) {
            pendingForm.querySelectorAll(".gs-loading").forEach(function (button) {
                button.classList.remove("gs-loading");
            });
        }
        pendingForm = null;
    };

    document.addEventListener("submit", function (event) {
        const form = event.target;
        if (!(form instanceof HTMLFormElement) || form.dataset.confirmed === "true") return;

        const button = form.querySelector("[data-confirm]");
        if (!button) return;

        event.preventDefault();
        pendingForm = form;
        const warning = button.dataset.confirmTone === "warning";
        modal.classList.toggle("modal-confirm-warning", warning);
        iconEl.innerHTML = warning ? warningIcon : trashIcon;
        titleEl.textContent = button.dataset.confirmTitle || "Confirmar remoção";
        confirmButton.textContent = button.dataset.confirmOk || "Remover";
        textEl.textContent = button.dataset.confirm;
        modal.hidden = false;
    });

    confirmButton.addEventListener("click", function () {
        if (!pendingForm) return;
        const form = pendingForm;
        const originalButton = form.querySelector("[data-confirm]");
        close();
        form.dataset.confirmed = "true";
        if (originalButton) originalButton.classList.add("gs-loading");
        form.submit();
    });

    cancelButton.addEventListener("click", close);
    modal.addEventListener("click", function (event) {
        if (event.target === modal) close();
    });
    document.addEventListener("keydown", function (event) {
        if (event.key === "Escape" && !modal.hidden) close();
    });
}

// Transforma as mensagens de sucesso e erro que vêm do servidor
// (.form-success e .form-error) em toasts que flutuam e somem sozinhos, em
// vez de ocuparem espaço fixo no topo da página.
function enableToasts() {
    const messages = document.querySelectorAll(".form-success, .form-error");
    if (!messages.length) return;

    let container = document.getElementById("gs-toasts");
    if (!container) {
        container = document.createElement("div");
        container.id = "gs-toasts";
        container.className = "gs-toasts";
        document.body.appendChild(container);
    }

    const successIcon = '<svg viewBox="0 0 24 24"><path d="M20 6 9 17l-5-5"></path></svg>';
    const errorIcon = '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>';

    messages.forEach(function (original) {
        const text = original.textContent.trim();
        if (!text) return;

        const isError = original.classList.contains("form-error");
        original.hidden = true;

        const toast = document.createElement("div");
        toast.className = "gs-toast " + (isError ? "gs-toast-error" : "gs-toast-success");
        toast.setAttribute("role", isError ? "alert" : "status");
        toast.innerHTML =
            '<span class="gs-toast-icon" aria-hidden="true">' + (isError ? errorIcon : successIcon) + "</span>" +
            '<span class="gs-toast-text"></span>' +
            '<button type="button" class="gs-toast-close" aria-label="Fechar">&times;</button>';
        toast.querySelector(".gs-toast-text").textContent = text;

        container.appendChild(toast);
        requestAnimationFrame(function () { toast.classList.add("gs-toast-visible"); });

        let timer;
        const remove = function () {
            clearTimeout(timer);
            toast.classList.remove("gs-toast-visible");
            toast.classList.add("gs-toast-leaving");
            setTimeout(function () { toast.remove(); }, 240);
        };

        toast.querySelector(".gs-toast-close").addEventListener("click", remove);
        toast.addEventListener("mouseenter", function () { clearTimeout(timer); });
        toast.addEventListener("mouseleave", function () { timer = setTimeout(remove, 2500); });
        timer = setTimeout(remove, isError ? 6000 : 4200);
    });
}

// Marca, com <mark>, o trecho do texto que bateu com a busca. Só mexe em
// texto puro: célula com botão, campo ou link fica como está.
function highlightSearch(element, term) {
    if (!element) return;
    clearHighlight(element);

    const cleanTerm = (term || "").trim();
    if (!cleanTerm) return;
    if (element.querySelector("button, input, select, form, a")) return;

    const termLower = cleanTerm.toLowerCase();
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const nodes = [];
    let node;
    while ((node = walker.nextNode())) nodes.push(node);

    nodes.forEach(function (textNode) {
        const text = textNode.textContent;
        const index = text.toLowerCase().indexOf(termLower);
        if (index === -1) return;

        const span = document.createElement("span");
        span.className = "gs-highlight-wrap";
        span.appendChild(document.createTextNode(text.slice(0, index)));
        const mark = document.createElement("mark");
        mark.className = "gs-highlight";
        mark.textContent = text.slice(index, index + cleanTerm.length);
        span.appendChild(mark);
        span.appendChild(document.createTextNode(text.slice(index + cleanTerm.length)));
        textNode.replaceWith(span);
    });
}

// Desfaz o destaque do highlightSearch e deixa só o texto.
function clearHighlight(element) {
    if (!element) return;
    element.querySelectorAll(".gs-highlight-wrap").forEach(function (span) {
        span.replaceWith(document.createTextNode(span.textContent));
    });
    element.normalize();
}

// Quando a página abre com uma busca na URL (?busca=), destaca o termo nas
// células das tabelas.
function enableSearchHighlightFromUrl() {
    const term = new URLSearchParams(window.location.search).get("busca");
    if (!term || !term.trim()) return;

    document.querySelectorAll(".content table tbody td").forEach(function (cell) {
        highlightSearch(cell, term);
    });
}

// Dá um pulso rápido num elemento, para chamar atenção quando um contador
// muda. O "void element.offsetWidth" força o navegador a recalcular o
// layout, e é isso que faz a animação recomeçar do zero.
function pulse(element) {
    if (!element) return;
    element.classList.remove("gs-pulse");
    void element.offsetWidth;
    element.classList.add("gs-pulse");
}

window.gsSearch = { highlight: highlightSearch, clear: clearHighlight };
window.gsPulse = pulse;
