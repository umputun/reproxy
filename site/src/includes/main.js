const sidebarElement = document.getElementById('sidebar')
const sidebarOpenButton = document.getElementById('sidebar-open-button')
const sidebarCloseButton = document.getElementById('sidebar-close-button')
const sidebarLinks = sidebarElement.querySelectorAll('a')

sidebarOpenButton.addEventListener('click', toggleSidebar)
sidebarCloseButton.addEventListener('click', closeSidebar)

Array.from(sidebarLinks).forEach((l) => {
	l.addEventListener('click', () => {
		if (!isShow()) {
			return
		}
		closeSidebar()
	})
})

function openSidebar() {
	document.body.classList.add('overflow-hidden')
	sidebarElement.classList.remove('hidden')
}

function closeSidebar() {
	sidebarElement.classList.add('hidden')
	document.body.classList.remove('overflow-hidden')
}

function isShow() {
	return !sidebarElement.classList.contains('hidden')
}

function toggleSidebar() {
	if (isShow()) {
		closeSidebar()
	} else {
		openSidebar()
	}
}
