import { SiteService } from '../bindings/webdesk/index.js'
import { Window } from '@wailsio/runtime'

let sites = []
let editingSiteId = null
let contextSiteId = null
let hasChrome = false
let currentSiteId = null
let openSiteUrls = new Set()
let isMaximised = false

const $ = (sel) => document.querySelector(sel)
const $$ = (sel) => document.querySelectorAll(sel)

function updateTitle(text) {
    $('#titlebar-title').textContent = text
    SiteService.SetTitle(text)
}

async function initTitlebar() {
    try {
        isMaximised = await Window.IsMaximised()
    } catch (e) {}
    updateMaximizeIcon()

    $('#btn-minimize').addEventListener('click', () => Window.Minimise())
    $('#btn-maximize').addEventListener('click', async () => {
        try {
            await Window.ToggleMaximise()
            isMaximised = !isMaximised
            updateMaximizeIcon()
        } catch (e) {}
    })
    $('#btn-close').addEventListener('click', () => Window.Close())

    // Listen for maximize/restore changes (e.g. double-click drag area)
    setInterval(async () => {
        try {
            const maximised = await Window.IsMaximised()
            if (maximised !== isMaximised) {
                isMaximised = maximised
                updateMaximizeIcon()
            }
        } catch (e) {}
    }, 500)
}

function updateMaximizeIcon() {
    const btn = $('#btn-maximize')
    if (isMaximised) {
        btn.title = '还原'
        btn.innerHTML = '<svg width="12" height="12" viewBox="0 0 12 12"><rect x="3" y="1" width="7" height="7" rx="1" stroke="currentColor" stroke-width="1.2" fill="none"/><rect x="1" y="4" width="7" height="7" rx="1" stroke="currentColor" stroke-width="1.2" fill="var(--bg-secondary)"/></svg>'
    } else {
        btn.title = '最大化'
        btn.innerHTML = '<svg width="12" height="12" viewBox="0 0 12 12"><rect x="2" y="2" width="8" height="8" rx="1" stroke="currentColor" stroke-width="1.2" fill="none"/></svg>'
    }
}

async function initTheme() {
    let saved = 'indigo'
    try {
        saved = await SiteService.LoadSetting('theme') || 'indigo'
    } catch (e) {}
    document.documentElement.setAttribute('data-theme', saved)
    document.querySelectorAll('.theme-dot').forEach(d => {
        d.classList.toggle('active', d.dataset.theme === saved)
    })
}

async function init() {
    try {
        hasChrome = await SiteService.HasChrome()
    } catch (e) {
        hasChrome = false
    }
    await loadSites()
    bindEvents()
    initTheme()
    initTitlebar()
    handleOpenParam()
    startIPCPolling()
    startOpenStatePolling()
}

function startOpenStatePolling() {
    setInterval(async () => {
        try {
            for (const site of sites) {
                if (site.openMode === 'chrome') {
                    const isOpen = await SiteService.IsSiteOpen(site.url)
                    if (isOpen) {
                        openSiteUrls.add(site.url)
                    } else {
                        openSiteUrls.delete(site.url)
                    }
                }
            }
            updateOpenIndicators()
        } catch (e) {}
    }, 2000)
}

function updateOpenIndicators() {
    document.querySelectorAll('.site-item').forEach(el => {
        const id = el.dataset.id
        const site = sites.find(s => s.id === id)
        if (!site || site.openMode !== 'chrome') return
        const dot = el.querySelector('.open-dot')
        if (openSiteUrls.has(site.url)) {
            if (!dot) {
                const d = document.createElement('span')
                d.className = 'open-dot'
                el.querySelector('.site-icon').appendChild(d)
            }
        } else if (dot) {
            dot.remove()
        }
    })
}

function startIPCPolling() {
    setInterval(async () => {
        try {
            const url = await SiteService.PopPendingOpen()
            if (url) {
                const site = sites.find(s => s.url === url)
                if (site) {
                    openSite(site.id)
                    if (site.openMode !== 'chrome') {
                        SiteService.WindowFocus()
                    }
                } else {
                    SiteService.SetTitle(url + ' - WebDesk')
                    updateTitle(url + ' - WebDesk')
                    document.getElementById('welcome-screen').style.display = 'none'
                    const frame = document.getElementById('site-frame')
                    frame.style.display = 'block'
                    frame.src = 'about:blank'
                    requestAnimationFrame(() => { frame.src = url })
                    SiteService.WindowFocus()
                }
            }
        } catch (e) {}
    }, 500)
}

function handleOpenParam() {
    const params = new URLSearchParams(window.location.search)
    const openUrl = params.get('open')
    if (!openUrl) return
    const site = sites.find(s => s.url === openUrl || s.url === openUrl.replace(/^https?:\/\//, 'https://'))
    if (site) {
        openSite(site.id)
    }
}

async function loadSites() {
    try {
        sites = await SiteService.GetAll()
    } catch (e) {
        sites = []
    }
    renderSidebar()
    renderWelcomeShortcuts()
    updateCategoryDatalist()
}

function renderSidebar() {
    const container = $('#sites-container')
    const searchTerm = $('#search-input').value.toLowerCase().trim()

    const filtered = sites.filter(s =>
        !searchTerm ||
        s.name.toLowerCase().includes(searchTerm) ||
        s.url.toLowerCase().includes(searchTerm) ||
        s.category.toLowerCase().includes(searchTerm)
    )

    const groups = {}
    filtered.forEach(site => {
        const cat = site.category || '未分类'
        if (!groups[cat]) groups[cat] = []
        groups[cat].push(site)
    })

    let html = ''
    for (const [category, items] of Object.entries(groups)) {
        html += `<div class="category-group">`
        html += `<div class="category-header">${escapeHtml(category)}</div>`
        items.forEach(site => {
            const modeTag = site.openMode === 'chrome' ? '<span class="mode-badge">Chrome</span>' : ''
            html += `
                <div class="site-item" data-id="${site.id}">
                    <div class="site-icon">${site.icon || '🌐'}</div>
                    <div class="site-info">
                        <div class="site-name">${escapeHtml(site.name)} ${modeTag}</div>
                        <div class="site-url-preview">${escapeHtml(shortenUrl(site.url))}</div>
                    </div>
                    <span class="context-trigger" data-id="${site.id}">⋮</span>
                </div>
            `
        })
        html += `</div>`
    }

    container.innerHTML = html || '<div style="padding:20px;text-align:center;color:var(--text-muted);font-size:13px;">暂无站点</div>'

    container.querySelectorAll('.site-item').forEach(el => {
        el.addEventListener('click', (e) => {
            if (e.target.classList.contains('context-trigger')) return
            openSite(el.dataset.id)
        })
    })

    container.querySelectorAll('.context-trigger').forEach(el => {
        el.addEventListener('click', (e) => {
            e.stopPropagation()
            showContextMenu(el.dataset.id, e)
        })
    })
}

function renderWelcomeShortcuts() {
    const container = $('#welcome-shortcuts')
    const shortcuts = sites.slice(0, 8)
    container.innerHTML = shortcuts.map(s => {
        const modeTag = s.openMode === 'chrome' ? ' 🖥️' : ''
        return `
            <div class="welcome-shortcut" data-id="${s.id}">
                <span class="welcome-shortcut-icon">${s.icon || '🌐'}</span>
                <span>${escapeHtml(s.name)}${modeTag}</span>
            </div>
        `
    }).join('')

    container.querySelectorAll('.welcome-shortcut').forEach(el => {
        el.addEventListener('click', () => openSite(el.dataset.id))
    })
}

function updateCategoryDatalist() {
    const datalist = $('#category-list')
    const cats = [...new Set(sites.map(s => s.category).filter(Boolean))]
    datalist.innerHTML = cats.map(c => `<option value="${escapeHtml(c)}">`).join('')
}

async function openSite(id) {
    const site = sites.find(s => s.id === id)
    if (!site) return

    if (site.openMode === 'chrome') {
        if (hasChrome) {
            SiteService.OpenSite(site.url)
        } else {
            SiteService.OpenInBrowser(site.url)
        }
        return
    }

    if (currentSiteId === id) return
    currentSiteId = id

    SiteService.SetTitle(site.name + ' - WebDesk')
    updateTitle(site.name + ' - WebDesk')

    $('#welcome-screen').style.display = 'none'
    const frame = $('#site-frame')
    frame.style.display = 'block'
    frame.src = 'about:blank'
    requestAnimationFrame(() => {
        frame.src = site.url
    })
}

async function createShortcut(id) {
    const site = sites.find(s => s.id === id)
    if (!site) return
    try {
        await SiteService.CreateShortcut(id)
        hideModal()
    } catch (e) {
        console.error('Create shortcut failed:', e)
    }
}

function openInChrome(id) {
    const site = sites.find(s => s.id === id)
    if (!site) return
    if (hasChrome) {
        SiteService.OpenSite(site.url)
    } else {
        SiteService.OpenInBrowser(site.url)
    }
    hideModal()
}

function showAddModal() {
    editingSiteId = null
    $('#modal-title').textContent = '添加站点'
    $('#input-name').value = ''
    $('#input-url').value = ''
    $('#input-icon').value = '🌐'
    $('#input-category').value = '常用'
    $('#input-openmode').value = 'embed'
    $('#site-modal').style.display = ''
    $('#context-menu').style.display = 'none'
    $('#modal-overlay').classList.add('active')
    setTimeout(() => $('#input-name').focus(), 100)
}

function showEditModal(id) {
    const site = sites.find(s => s.id === id)
    if (!site) return

    editingSiteId = id
    $('#modal-title').textContent = '编辑站点'
    $('#input-name').value = site.name
    $('#input-url').value = site.url
    $('#input-icon').value = site.icon
    $('#input-category').value = site.category
    $('#input-openmode').value = site.openMode || 'embed'
    $('#site-modal').style.display = ''
    $('#context-menu').style.display = 'none'
    $('#modal-overlay').classList.add('active')
    setTimeout(() => $('#input-name').focus(), 100)
}

function hideModal() {
    $('#modal-overlay').classList.remove('active')
    $('#context-menu').style.display = 'none'
    $('#site-modal').style.display = ''
    editingSiteId = null
    contextSiteId = null
}

async function saveSite() {
    const name = $('#input-name').value.trim()
    const url = $('#input-url').value.trim()
    const icon = $('#input-icon').value.trim() || '🌐'
    const category = $('#input-category').value.trim() || '常用'
    const openMode = $('#input-openmode').value || 'embed'

    if (!name || !url) return

    try {
        if (editingSiteId) {
            await SiteService.Update(editingSiteId, name, url, icon, category, openMode)
        } else {
            await SiteService.Create(name, url, icon, category, openMode)
        }
        await loadSites()
        hideModal()
    } catch (e) {
        console.error('Save failed:', e)
    }
}

async function deleteSite(id) {
    try {
        await SiteService.Delete(id)
        const frame = $('#site-frame')
        const site = sites.find(s => s.id === id)
        if (site && frame.src && frame.src !== 'about:blank') {
            try {
                const frameHost = new URL(frame.src).hostname
                const siteHost = new URL(site.url).hostname
                if (frameHost === siteHost) {
                    frame.style.display = 'none'
                    frame.src = 'about:blank'
                    $('#welcome-screen').style.display = 'flex'
                    SiteService.SetTitle('WebDesk')
                    updateTitle('WebDesk')
                }
            } catch(e) {}
        }
        await loadSites()
    } catch (e) {
        console.error('Delete failed:', e)
    }
}

function showContextMenu(id, e) {
    contextSiteId = id
    const menu = $('#context-menu')
    const chromeBtn = $('#ctx-chrome')
    chromeBtn.style.display = hasChrome ? '' : 'none'

    menu.style.left = e.clientX + 'px'
    menu.style.top = e.clientY + 'px'
    menu.style.position = 'fixed'
    menu.style.display = 'block'
    $('#site-modal').style.display = 'none'
    $('#modal-overlay').classList.add('active')
}

function bindEvents() {
    $('#btn-add-site').addEventListener('click', showAddModal)
    $('#btn-modal-close').addEventListener('click', hideModal)
    $('#btn-cancel').addEventListener('click', hideModal)
    $('#btn-save').addEventListener('click', saveSite)

    $('#modal-overlay').addEventListener('click', (e) => {
        if (e.target === $('#modal-overlay')) {
            hideModal()
        }
    })

    $$('.icon-option').forEach(el => {
        el.addEventListener('click', () => {
            $('#input-icon').value = el.dataset.icon
        })
    })

    $('#input-name').addEventListener('keydown', (e) => {
        if (e.key === 'Enter') saveSite()
    })

    $('#input-url').addEventListener('keydown', (e) => {
        if (e.key === 'Enter') saveSite()
    })

    $('#ctx-edit').addEventListener('click', () => {
        if (contextSiteId) showEditModal(contextSiteId)
    })

    $('#ctx-chrome').addEventListener('click', () => {
        if (contextSiteId) openInChrome(contextSiteId)
    })

    $('#ctx-shortcut').addEventListener('click', () => {
        if (contextSiteId) createShortcut(contextSiteId)
    })

    $('#ctx-delete').addEventListener('click', () => {
        if (contextSiteId) {
            deleteSite(contextSiteId)
            hideModal()
        }
    })

    $('#btn-sidebar-toggle').addEventListener('click', () => {
        $('#sidebar').classList.toggle('collapsed')
    })

    $('#search-input').addEventListener('input', () => {
        renderSidebar()
    })

    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            hideModal()
        }
        if ((e.ctrlKey || e.metaKey) && e.key === 'n') {
            e.preventDefault()
            showAddModal()
        }
    })

    document.querySelectorAll('.theme-dot').forEach(dot => {
        dot.addEventListener('click', () => {
            const theme = dot.dataset.theme
            document.documentElement.setAttribute('data-theme', theme)
            SiteService.SaveSettings('theme', theme)
            document.querySelectorAll('.theme-dot').forEach(d => d.classList.remove('active'))
            dot.classList.add('active')
        })
    })

    // Opacity slider
    const opacitySlider = $('#opacity-slider')
    initOpacity()
    opacitySlider.addEventListener('input', () => {
        const val = opacitySlider.value / 100
        SiteService.SetOpacity(val)
        SiteService.SaveSettings('opacity', String(opacitySlider.value))
    })
}

async function initOpacity() {
    const opacitySlider = $('#opacity-slider')
    try {
        const saved = await SiteService.LoadSetting('opacity')
        if (saved) {
            opacitySlider.value = saved
            SiteService.SetOpacity(saved / 100)
        }
    } catch (e) {}
}

function escapeHtml(str) {
    if (!str) return ''
    const div = document.createElement('div')
    div.textContent = str
    return div.innerHTML
}

function shortenUrl(url) {
    try {
        const u = new URL(url)
        return u.hostname
    } catch {
        return url.replace(/^https?:\/\//, '').split('/')[0]
    }
}

init()
